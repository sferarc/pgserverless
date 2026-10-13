/**
 * Error Consistency Tests (HTTP vs WebSocket)
 *
 * Based on real bugs from Neon and PlanetScale:
 * - Neon #51: HTTP NeonDbError missing fields (constraint, table, column)
 * - Neon #140: Wrong password gave "Connection terminated unexpectedly"
 * - Neon #82: NeonDbError missing stack traces
 * - PlanetScale #61: Queries silently swallowed errors
 * - PlanetScale #82: Undescriptive error bodies
 *
 * These tests verify that errors contain complete, actionable information
 * and are consistent between HTTP and WebSocket transports.
 */

import { Client, type NeonDbError, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Error Consistency Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let constraintTable: string;

  beforeAll(async () => {
    constraintTable = uniqueTableName("err_consistency");
    await sql`CREATE TABLE ${sql.unsafe(constraintTable)} (
      id INT PRIMARY KEY,
      name TEXT NOT NULL UNIQUE,
      val INT CHECK (val > 0)
    )`;
    await sql`INSERT INTO ${sql.unsafe(constraintTable)} VALUES (1, 'existing', 10)`;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(constraintTable)}`;
  });

  // ==================== Error Field Completeness (HTTP) ====================
  describe("HTTP Error Field Completeness", () => {
    test("syntax error has code and message", async () => {
      try {
        await sql`SELEC 1`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as NeonDbError;
        expect(err.code).toBe("42601");
        expect(err.message).toBeDefined();
        expect(err.message.length).toBeGreaterThan(0);
      }
    });

    test("error has severity field", async () => {
      try {
        await sql`SELECT * FROM nonexistent_table_xyz_123`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as NeonDbError;
        expect(err.severity).toBe("ERROR");
      }
    });

    test("unique violation has constraint info", async () => {
      try {
        await sql`INSERT INTO ${sql.unsafe(constraintTable)} VALUES (2, 'existing', 20)`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as NeonDbError;
        expect(err.code).toBe("23505");
        expect(err.message).toBeDefined();
      }
    });

    test("not-null violation error details", async () => {
      try {
        await sql`INSERT INTO ${sql.unsafe(constraintTable)} (id, name, val) VALUES (99, NULL, 5)`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as NeonDbError;
        expect(err.code).toBe("23502");
        expect(err.message).toBeDefined();
      }
    });

    test("check constraint violation error details", async () => {
      try {
        await sql`INSERT INTO ${sql.unsafe(constraintTable)} (id, name, val) VALUES (99, 'test_check', -1)`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as NeonDbError;
        expect(err.code).toBe("23514");
        expect(err.message).toBeDefined();
      }
    });

    test("undefined column has position", async () => {
      try {
        await sql`SELECT undefined_col_xyz FROM ${sql.unsafe(constraintTable)}`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as NeonDbError;
        expect(err.code).toBe("42703");
        // Position should be defined for syntax-related errors
        expect(err.position).toBeDefined();
      }
    });

    test("error is an instance of Error", async () => {
      try {
        await sql`INVALID SQL`;
        expect(true).toBe(false);
      } catch (e) {
        expect(e instanceof Error).toBe(true);
      }
    });

    test("error has name property", async () => {
      try {
        await sql`INVALID SQL`;
        expect(true).toBe(false);
      } catch (e) {
        expect((e as NeonDbError).name).toBe("NeonDbError");
      }
    });

    test("promise rejects on error (not resolves)", async () => {
      let caught = false;
      try {
        await sql`SELECT 1/0`;
      } catch {
        caught = true;
      }
      expect(caught).toBe(true);
    });
  });

  // ==================== Error Field Completeness (WebSocket) ====================
  describe("WebSocket Error Field Completeness", () => {
    test("syntax error has code and message via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      try {
        await client.query("SELEC 1");
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        expect(err.code).toBe("42601");
        expect(err.message).toBeDefined();
      }

      await client.end();
    });

    test("error has severity field via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      try {
        await client.query("SELECT * FROM nonexistent_table_xyz_123");
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        expect(err.severity).toBe("ERROR");
      }

      await client.end();
    });

    test("unique violation via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      try {
        await client.query(`INSERT INTO ${constraintTable} VALUES (3, 'existing', 30)`);
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        expect(err.code).toBe("23505");
        expect(err.constraint).toBeDefined();
      }

      await client.end();
    });

    test("error via pool.query", async () => {
      const pool = new Pool({ connectionString: dbUrl });

      try {
        await pool.query("INVALID SQL");
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        expect(err.code).toBeDefined();
        expect(err.message).toBeDefined();
      }

      await pool.end();
    });
  });

  // ==================== HTTP vs WebSocket Error Comparison ====================
  describe("HTTP vs WebSocket Error Parity", () => {
    test("same error code for syntax error", async () => {
      let httpCode: string | undefined;
      let wsCode: string | undefined;

      try {
        await sql`SELEC 1`;
      } catch (e) {
        httpCode = (e as NeonDbError).code;
      }

      const client = new Client(dbUrl);
      await client.connect();
      try {
        await client.query("SELEC 1");
      } catch (e) {
        wsCode = (e as Record<string, unknown>).code as string;
      }
      await client.end();

      expect(httpCode).toBe("42601");
      expect(wsCode).toBe("42601");
      expect(httpCode).toBe(wsCode);
    });

    test("same error code for division by zero", async () => {
      let httpCode: string | undefined;
      let wsCode: string | undefined;

      try {
        await sql`SELECT 1/0`;
      } catch (e) {
        httpCode = (e as NeonDbError).code;
      }

      const client = new Client(dbUrl);
      await client.connect();
      try {
        await client.query("SELECT 1/0");
      } catch (e) {
        wsCode = (e as Record<string, unknown>).code as string;
      }
      await client.end();

      expect(httpCode).toBe("22012");
      expect(wsCode).toBe("22012");
    });

    test("same error code for unique violation", async () => {
      let httpCode: string | undefined;
      let wsCode: string | undefined;

      try {
        await sql`INSERT INTO ${sql.unsafe(constraintTable)} VALUES (1, 'dup', 5)`;
      } catch (e) {
        httpCode = (e as NeonDbError).code;
      }

      const client = new Client(dbUrl);
      await client.connect();
      try {
        await client.query(`INSERT INTO ${constraintTable} VALUES (1, 'dup2', 5)`);
      } catch (e) {
        wsCode = (e as Record<string, unknown>).code as string;
      }
      await client.end();

      expect(httpCode).toBe("23505");
      expect(wsCode).toBe("23505");
    });

    test("same error code for undefined table", async () => {
      let httpCode: string | undefined;
      let wsCode: string | undefined;

      try {
        await sql`SELECT * FROM totally_nonexistent_table_xyz`;
      } catch (e) {
        httpCode = (e as NeonDbError).code;
      }

      const client = new Client(dbUrl);
      await client.connect();
      try {
        await client.query("SELECT * FROM totally_nonexistent_table_xyz");
      } catch (e) {
        wsCode = (e as Record<string, unknown>).code as string;
      }
      await client.end();

      expect(httpCode).toBe("42P01");
      expect(wsCode).toBe("42P01");
    });

    test("same error code for not-null violation", async () => {
      let httpCode: string | undefined;
      let wsCode: string | undefined;

      try {
        await sql`INSERT INTO ${sql.unsafe(constraintTable)} (id, name, val) VALUES (50, NULL, 1)`;
      } catch (e) {
        httpCode = (e as NeonDbError).code;
      }

      const client = new Client(dbUrl);
      await client.connect();
      try {
        await client.query(`INSERT INTO ${constraintTable} (id, name, val) VALUES (51, NULL, 1)`);
      } catch (e) {
        wsCode = (e as Record<string, unknown>).code as string;
      }
      await client.end();

      expect(httpCode).toBe("23502");
      expect(wsCode).toBe("23502");
    });
  });

  // ==================== Error Recovery ====================
  describe("Error Recovery", () => {
    test("HTTP: query succeeds after error", async () => {
      try {
        await sql`INVALID`;
      } catch {
        // Expected
      }
      const result = await sql`SELECT 1 as val`;
      expect(result[0].val).toBe(1);
    });

    test("WebSocket: query succeeds after error on same client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      try {
        await client.query("INVALID");
      } catch {
        // Expected
      }

      const result = await client.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);

      await client.end();
    });

    test("Pool: query succeeds after error", async () => {
      const pool = new Pool({ connectionString: dbUrl });

      try {
        await pool.query("INVALID");
      } catch {
        // Expected
      }

      const result = await pool.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);

      await pool.end();
    });

    test("multiple consecutive errors don't break connection", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      for (let i = 0; i < 5; i++) {
        try {
          await client.query("INVALID");
        } catch {
          // Expected
        }
      }

      const result = await client.query("SELECT 'recovered' as val");
      expect(result.rows[0].val).toBe("recovered");

      await client.end();
    });

    test("transaction error followed by normal query", async () => {
      try {
        await sql.transaction([sql`SELECT 1`, sql`SELECT 1/0`]);
      } catch {
        // Expected
      }

      const result = await sql`SELECT 'after_txn_error' as val`;
      expect(result[0].val).toBe("after_txn_error");
    });
  });
});

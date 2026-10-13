/**
 * Prepared Statement Concurrency Tests
 *
 * Based on real bugs from Neon:
 * - Neon #144: Proxy prepared statement name collision (~50% failure rate)
 * - Neon #53: PgBouncer connection recycling invalidated prepared statements
 * - Neon #28: Bind message parameter count mismatch with ORM query formats
 *
 * These tests verify prepared statements work correctly under
 * concurrent load and with various parameter passing patterns.
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Prepared Statement Concurrency Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("prep_stmt");
    await sql`CREATE TABLE ${sql.unsafe(testTable)} (
      id SERIAL PRIMARY KEY,
      name TEXT,
      value INTEGER
    )`;
    // Insert some test data
    for (let i = 0; i < 20; i++) {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${`item_${i}`}, ${i})`;
    }
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Parallel Parameterized Queries ====================
  describe("Parallel Parameterized Queries", () => {
    test("10 parallel parameterized queries via Pool", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = Array.from({ length: 10 }, (_, i) =>
        pool.query(`SELECT $1::integer as num, $2::text as label`, [i, `query_${i}`]),
      );

      const results = await Promise.all(queries);
      for (let i = 0; i < 10; i++) {
        expect(results[i].rows[0].num).toBe(i);
        expect(results[i].rows[0].label).toBe(`query_${i}`);
      }

      await pool.end();
    });

    test("20 parallel parameterized queries via Pool", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = Array.from({ length: 20 }, (_, i) =>
        pool.query(`SELECT * FROM ${testTable} WHERE value = $1`, [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(20);
      for (let i = 0; i < 20; i++) {
        expect(results[i].rows.length).toBe(1);
        expect(results[i].rows[0].value).toBe(i);
      }

      await pool.end();
    });

    test("parallel queries with different parameter types", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = [
        pool.query("SELECT $1::integer as val", [42]),
        pool.query("SELECT $1::text as val", ["hello"]),
        pool.query("SELECT $1::boolean as val", [true]),
        pool.query("SELECT $1::float8 as val", [3.14]),
        pool.query("SELECT $1::integer as val", [0]),
        pool.query("SELECT $1::text as val", [""]),
        pool.query("SELECT $1::boolean as val", [false]),
        pool.query("SELECT $1::integer[] as val", [[1, 2, 3]]),
      ];

      const results = await Promise.all(queries);
      expect(results[0].rows[0].val).toBe(42);
      expect(results[1].rows[0].val).toBe("hello");
      expect(results[2].rows[0].val).toBe(true);
      expect(results[3].rows[0].val).toBeCloseTo(3.14);
      expect(results[4].rows[0].val).toBe(0);
      expect(results[5].rows[0].val).toBe("");
      expect(results[6].rows[0].val).toBe(false);
      expect(results[7].rows[0].val).toEqual([1, 2, 3]);

      await pool.end();
    });

    test("rapid-fire sequential parameterized queries on single client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      for (let i = 0; i < 20; i++) {
        const result = await client.query("SELECT $1::integer as num", [i]);
        expect(result.rows[0].num).toBe(i);
      }

      await client.end();
    });

    test("parallel queries after pool connection recycling", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 2 });

      // First batch: establish connections
      await Promise.all([pool.query("SELECT 1"), pool.query("SELECT 2")]);

      // Second batch: connections should be recycled
      const queries = Array.from({ length: 10 }, (_, i) =>
        pool.query("SELECT $1::integer as num", [i]),
      );

      const results = await Promise.all(queries);
      for (let i = 0; i < 10; i++) {
        expect(results[i].rows[0].num).toBe(i);
      }

      await pool.end();
    });
  });

  // ==================== Parameter Format Variations ====================
  describe("Parameter Format Variations (ORM compatibility)", () => {
    test("query(text, values) - standard format", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      const result = await pool.query("SELECT $1::text as val", ["hello"]);
      expect(result.rows[0].val).toBe("hello");
      await pool.end();
    });

    test("query({ text, values }) - config object format", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      const result = await pool.query({
        text: "SELECT $1::text as val",
        values: ["hello"],
      });
      expect(result.rows[0].val).toBe("hello");
      await pool.end();
    });

    test("query({ text }, values) - hybrid format (Drizzle-style)", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      // This is the format Drizzle ORM uses, which broke in Neon #28
      const result = await pool.query({ text: "SELECT $1::text as val" }, ["hello"]);
      expect(result.rows[0].val).toBe("hello");
      await pool.end();
    });

    test("query with no parameters", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      const result = await pool.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);
      await pool.end();
    });

    test("query with empty parameter array", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      const result = await pool.query("SELECT 1 as val", []);
      expect(result.rows[0].val).toBe(1);
      await pool.end();
    });

    test("query with many parameters", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      const params = Array.from({ length: 20 }, (_, i) => i);
      const placeholders = params.map((_, i) => `$${i + 1}::integer`).join(", ");
      const result = await pool.query(`SELECT ${placeholders}`, params);
      expect(result.rows.length).toBe(1);
      await pool.end();
    });
  });

  // ==================== Named Prepared Statements ====================
  describe("Named Prepared Statements", () => {
    test("named statement with Client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const result = await client.query({
        name: "get-value",
        text: `SELECT * FROM ${testTable} WHERE value = $1`,
        values: [5],
      });
      expect(result.rows[0].value).toBe(5);

      // Reuse same named statement
      const result2 = await client.query({
        name: "get-value",
        text: `SELECT * FROM ${testTable} WHERE value = $1`,
        values: [10],
      });
      expect(result2.rows[0].value).toBe(10);

      await client.end();
    });

    test("different named statements don't interfere", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const r1 = await client.query({
        name: "stmt-a",
        text: "SELECT $1::integer as val",
        values: [1],
      });

      const r2 = await client.query({
        name: "stmt-b",
        text: "SELECT $1::text as val",
        values: ["hello"],
      });

      expect(r1.rows[0].val).toBe(1);
      expect(r2.rows[0].val).toBe("hello");

      await client.end();
    });
  });

  // ==================== Concurrent Pool Clients ====================
  describe("Concurrent Pool Client Parameterized Queries", () => {
    test("multiple pool clients with parameterized queries", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 3 });

      const work = async (id: number) => {
        const client = await pool.connect();
        try {
          for (let i = 0; i < 5; i++) {
            const result = await client.query(
              "SELECT $1::integer as client_id, $2::integer as iteration",
              [id, i],
            );
            expect(result.rows[0].client_id).toBe(id);
            expect(result.rows[0].iteration).toBe(i);
          }
        } finally {
          client.release();
        }
      };

      await Promise.all([work(1), work(2), work(3)]);

      await pool.end();
    });

    test("pool query stress with parameterized queries", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = Array.from({ length: 100 }, (_, i) =>
        pool.query("SELECT $1::integer as num, $2::text as label", [i % 20, `label_${i}`]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(100);
      for (let i = 0; i < 100; i++) {
        expect(results[i].rows[0].num).toBe(i % 20);
        expect(results[i].rows[0].label).toBe(`label_${i}`);
      }

      await pool.end();
    });
  });
});

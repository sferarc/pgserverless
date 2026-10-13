/**
 * Transaction Rollback Correctness Tests
 *
 * Based on real bugs from PlanetScale and Neon:
 * - PlanetScale #106: Transactions didn't actually roll back on error
 * - PlanetScale #124: Transaction locks not released on error
 * - PlanetScale #139: Promise.all() inside transactions silently dropped statements
 * - Neon #76: Parallel inserts lost transaction scope in Node 20
 *
 * These tests verify transaction atomicity, rollback correctness,
 * and proper lock release after errors.
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Transaction Rollback Correctness", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);

  // ==================== HTTP Transaction Rollback ====================
  describe("HTTP Transaction Rollback", () => {
    test("INSERT rolled back on subsequent error", async () => {
      const tbl = uniqueTableName("txn_rb_insert");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val TEXT)`;

      try {
        await sql.transaction([
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 'should_not_persist')`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 'duplicate')`, // PK violation
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const result = await sql`SELECT * FROM ${sql.unsafe(tbl)}`;
      expect(result.length).toBe(0);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("multiple INSERTs rolled back on later error", async () => {
      const tbl = uniqueTableName("txn_rb_multi");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val TEXT)`;

      try {
        await sql.transaction([
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 'a')`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (2, 'b')`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (3, 'c')`,
          sql`SELECT 1/0`, // Division by zero
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)}`;
      expect(Number(result[0].cnt)).toBe(0);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("UPDATE rolled back on error", async () => {
      const tbl = uniqueTableName("txn_rb_update");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 100)`;

      try {
        await sql.transaction([
          sql`UPDATE ${sql.unsafe(tbl)} SET val = 200 WHERE id = 1`,
          sql`SELECT 1/0`,
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const result = await sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`;
      expect(result[0].val).toBe(100); // Should retain original value

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("DELETE rolled back on error", async () => {
      const tbl = uniqueTableName("txn_rb_delete");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val TEXT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 'keep'), (2, 'keep'), (3, 'keep')`;

      try {
        await sql.transaction([
          sql`DELETE FROM ${sql.unsafe(tbl)} WHERE id = 1`,
          sql`DELETE FROM ${sql.unsafe(tbl)} WHERE id = 2`,
          sql`SELECT 1/0`,
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)}`;
      expect(Number(result[0].cnt)).toBe(3); // All rows should still exist

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("constraint violation mid-transaction rolls back all", async () => {
      const tbl = uniqueTableName("txn_rb_constraint");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val TEXT NOT NULL)`;

      try {
        await sql.transaction([
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 'valid')`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (2, NULL)`, // NOT NULL violation
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)}`;
      expect(Number(result[0].cnt)).toBe(0);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("error in first statement prevents all", async () => {
      const tbl = uniqueTableName("txn_rb_first");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY)`;

      try {
        await sql.transaction([
          sql`SELECT 1/0`, // Error first
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (2)`,
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)}`;
      expect(Number(result[0].cnt)).toBe(0);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== WebSocket Transaction Rollback ====================
  describe("WebSocket Transaction Rollback", () => {
    test("ROLLBACK undoes INSERT via client", async () => {
      const tbl = uniqueTableName("ws_rb_insert");
      const client = new Client(dbUrl);
      await client.connect();

      await client.query(`CREATE TABLE ${tbl} (id INT PRIMARY KEY, val TEXT)`);

      await client.query("BEGIN");
      await client.query(`INSERT INTO ${tbl} VALUES (1, 'should_not_persist')`);
      await client.query(`INSERT INTO ${tbl} VALUES (2, 'also_not')`);
      await client.query("ROLLBACK");

      const result = await client.query(`SELECT COUNT(*) as cnt FROM ${tbl}`);
      expect(Number(result.rows[0].cnt)).toBe(0);

      await client.query(`DROP TABLE ${tbl}`);
      await client.end();
    });

    test("error in transaction followed by ROLLBACK", async () => {
      const tbl = uniqueTableName("ws_rb_error");
      const client = new Client(dbUrl);
      await client.connect();

      await client.query(`CREATE TABLE ${tbl} (id INT PRIMARY KEY)`);

      await client.query("BEGIN");
      await client.query(`INSERT INTO ${tbl} VALUES (1)`);

      try {
        await client.query(`INSERT INTO ${tbl} VALUES (1)`); // PK violation
      } catch {
        // Expected
      }
      await client.query("ROLLBACK");

      const result = await client.query(`SELECT COUNT(*) as cnt FROM ${tbl}`);
      expect(Number(result.rows[0].cnt)).toBe(0);

      await client.query(`DROP TABLE ${tbl}`);
      await client.end();
    });

    test("queries work after rollback", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      await client.query("BEGIN");
      await client.query("SELECT 1");
      await client.query("ROLLBACK");

      // Should be able to query normally after rollback
      const result = await client.query("SELECT 42 as val");
      expect(result.rows[0].val).toBe(42);

      await client.end();
    });

    test("consecutive transactions after rollback", async () => {
      const tbl = uniqueTableName("ws_rb_consec");
      const client = new Client(dbUrl);
      await client.connect();

      await client.query(`CREATE TABLE ${tbl} (id INT PRIMARY KEY)`);

      // First transaction: rolled back
      await client.query("BEGIN");
      await client.query(`INSERT INTO ${tbl} VALUES (1)`);
      await client.query("ROLLBACK");

      // Second transaction: committed
      await client.query("BEGIN");
      await client.query(`INSERT INTO ${tbl} VALUES (2)`);
      await client.query("COMMIT");

      const result = await client.query(`SELECT * FROM ${tbl}`);
      expect(result.rows.length).toBe(1);
      expect(result.rows[0].id).toBe(2);

      await client.query(`DROP TABLE ${tbl}`);
      await client.end();
    });

    test("SAVEPOINT rollback preserves prior work", async () => {
      const tbl = uniqueTableName("ws_rb_savepoint");
      const client = new Client(dbUrl);
      await client.connect();

      await client.query(`CREATE TABLE ${tbl} (id INT PRIMARY KEY, val TEXT)`);

      await client.query("BEGIN");
      await client.query(`INSERT INTO ${tbl} VALUES (1, 'keep')`);
      await client.query("SAVEPOINT sp1");
      await client.query(`INSERT INTO ${tbl} VALUES (2, 'discard')`);
      await client.query("ROLLBACK TO SAVEPOINT sp1");
      await client.query(`INSERT INTO ${tbl} VALUES (3, 'keep_too')`);
      await client.query("COMMIT");

      const result = await client.query(`SELECT id, val FROM ${tbl} ORDER BY id`);
      expect(result.rows.length).toBe(2);
      expect(result.rows[0].id).toBe(1);
      expect(result.rows[1].id).toBe(3);

      await client.query(`DROP TABLE ${tbl}`);
      await client.end();
    });
  });

  // ==================== Lock Release After Error ====================
  describe("Lock Release After Transaction Error", () => {
    test("table accessible immediately after failed transaction", async () => {
      const tbl = uniqueTableName("lock_release");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 100)`;

      // Transaction that fails
      try {
        await sql.transaction([
          sql`UPDATE ${sql.unsafe(tbl)} SET val = 200 WHERE id = 1`,
          sql`SELECT 1/0`,
        ]);
      } catch {
        // Expected
      }

      // Should be immediately accessible, no lock wait
      const start = Date.now();
      const result = await sql`UPDATE ${sql.unsafe(tbl)} SET val = 300 WHERE id = 1 RETURNING val`;
      const elapsed = Date.now() - start;

      expect(result[0].val).toBe(300);
      // Should not take more than a few seconds (lock timeout is typically 20s)
      expect(elapsed).toBeLessThan(5000);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("concurrent transaction proceeds after failed one", async () => {
      const tbl = uniqueTableName("lock_concurrent");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 100)`;

      // First: fail
      try {
        await sql.transaction([
          sql`UPDATE ${sql.unsafe(tbl)} SET val = 200 WHERE id = 1`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 'duplicate')`, // PK violation
        ]);
      } catch {
        // Expected
      }

      // Second: should succeed without waiting for locks
      const results = await sql.transaction([
        sql`UPDATE ${sql.unsafe(tbl)} SET val = 300 WHERE id = 1`,
        sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`,
      ]);

      expect(asRows(results[1])[0].val).toBe(300);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Pool Transaction Isolation ====================
  describe("Pool Transaction Isolation", () => {
    test("concurrent pool transactions are isolated", async () => {
      const tbl = uniqueTableName("pool_iso");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 0)`;

      const pool = new Pool({ connectionString: dbUrl, max: 2 });

      // Two transactions updating the same row
      await Promise.all([
        (async () => {
          const client = await pool.connect();
          try {
            await client.query("BEGIN");
            await client.query(`UPDATE ${tbl} SET val = val + 10 WHERE id = 1`);
            await client.query("COMMIT");
            return client.query(`SELECT val FROM ${tbl} WHERE id = 1`);
          } finally {
            client.release();
          }
        })(),
        (async () => {
          const client = await pool.connect();
          try {
            await client.query("BEGIN");
            await client.query(`UPDATE ${tbl} SET val = val + 20 WHERE id = 1`);
            await client.query("COMMIT");
            return client.query(`SELECT val FROM ${tbl} WHERE id = 1`);
          } finally {
            client.release();
          }
        })(),
      ]);

      // Final value should be 30 (0 + 10 + 20)
      const final = await sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`;
      expect(final[0].val).toBe(30);

      await pool.end();
      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });
});

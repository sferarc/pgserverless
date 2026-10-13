/**
 * Concurrent HTTP Request Tests
 *
 * Based on real bugs from Neon and PlanetScale:
 * - Neon #56: Stale responses from fetch caching (Next.js)
 * - Neon #76: Parallel requests lost scope in Node 20
 * - Neon #144: Proxy prepared statement name collision under concurrency
 * - PlanetScale #139: Promise.all() inside transactions silently dropped statements
 *
 * These tests verify that concurrent HTTP queries return correct results
 * and don't interfere with each other.
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Concurrent HTTP Request Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("concurrent_http");
    await sql`CREATE TABLE ${sql.unsafe(testTable)} (
      id SERIAL PRIMARY KEY,
      name TEXT,
      value INTEGER
    )`;
    for (let i = 0; i < 20; i++) {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${`item_${i}`}, ${i})`;
    }
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Parallel Query Result Isolation ====================
  describe("Parallel Query Result Isolation", () => {
    test("10 parallel SELECT queries return distinct results", async () => {
      const queries = Array.from({ length: 10 }, (_, i) => sql`SELECT ${i}::integer as num`);

      const results = await Promise.all(queries);
      for (let i = 0; i < 10; i++) {
        expect(results[i][0].num).toBe(i);
      }
    });

    test("20 parallel queries with different types", async () => {
      const queries = [
        ...Array.from({ length: 5 }, (_, i) => sql`SELECT ${i}::integer as val`),
        ...Array.from({ length: 5 }, (_, i) => sql`SELECT ${`text_${i}`}::text as val`),
        ...Array.from({ length: 5 }, (_, i) => sql`SELECT ${i % 2 === 0}::boolean as val`),
        ...Array.from({ length: 5 }, (_, i) => sql`SELECT ${i * 1.5}::float8 as val`),
      ];

      const results = await Promise.all(queries);
      expect(results.length).toBe(20);

      // Verify integer results
      for (let i = 0; i < 5; i++) {
        expect(results[i][0].val).toBe(i);
      }
      // Verify text results
      for (let i = 5; i < 10; i++) {
        expect(results[i][0].val).toBe(`text_${i - 5}`);
      }
      // Verify boolean results
      for (let i = 10; i < 15; i++) {
        expect(typeof results[i][0].val).toBe("boolean");
      }
      // Verify float results
      for (let i = 15; i < 20; i++) {
        expect(typeof results[i][0].val).toBe("number");
      }
    });

    test("parallel reads from same table", async () => {
      const queries = Array.from(
        { length: 10 },
        (_, i) => sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE value = ${i}`,
      );

      const results = await Promise.all(queries);
      for (let i = 0; i < 10; i++) {
        expect(results[i].length).toBe(1);
        expect(results[i][0].value).toBe(i);
        expect(results[i][0].name).toBe(`item_${i}`);
      }
    });

    test("parallel queries returning different row counts", async () => {
      const queries = [
        sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE value < 3`,
        sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE value < 5`,
        sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE value < 10`,
        sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE value < 15`,
        sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE value < 20`,
      ];

      const results = await Promise.all(queries);
      expect(results[0].length).toBe(3);
      expect(results[1].length).toBe(5);
      expect(results[2].length).toBe(10);
      expect(results[3].length).toBe(15);
      expect(results[4].length).toBe(20);
    });
  });

  // ==================== Concurrent Reads and Writes ====================
  describe("Concurrent Reads and Writes", () => {
    test("parallel inserts don't interfere", async () => {
      const tbl = uniqueTableName("par_insert");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val TEXT)`;

      // Insert 10 rows in parallel
      const inserts = Array.from(
        { length: 10 },
        (_, i) => sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (${i}, ${`val_${i}`})`,
      );
      await Promise.all(inserts);

      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)}`;
      expect(Number(result[0].cnt)).toBe(10);

      // Verify all rows
      const rows = await sql`SELECT * FROM ${sql.unsafe(tbl)} ORDER BY id`;
      for (let i = 0; i < 10; i++) {
        expect(rows[i].id).toBe(i);
        expect(rows[i].val).toBe(`val_${i}`);
      }

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("read during write returns consistent data", async () => {
      const tbl = uniqueTableName("rw_consist");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 100)`;

      // Interleave reads and writes
      const operations = [
        sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`,
        sql`UPDATE ${sql.unsafe(tbl)} SET val = 200 WHERE id = 1`,
        sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`,
      ];

      const results = await Promise.all(operations);

      // First read might get 100 or 200 depending on timing
      // Second read might get 100 or 200 depending on timing
      // Both should be valid integers
      expect([100, 200]).toContain(results[0][0].val);
      expect([100, 200]).toContain(results[2][0].val);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Concurrent Transactions ====================
  describe("Concurrent HTTP Transactions", () => {
    test("parallel transactions are isolated", async () => {
      const tbl = uniqueTableName("par_txn");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 0)`;

      // Two transactions incrementing the same counter
      const txn1 = sql.transaction([
        sql`UPDATE ${sql.unsafe(tbl)} SET val = val + 10 WHERE id = 1`,
        sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`,
      ]);

      const txn2 = sql.transaction([
        sql`UPDATE ${sql.unsafe(tbl)} SET val = val + 20 WHERE id = 1`,
        sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`,
      ]);

      await Promise.all([txn1, txn2]);

      const final = await sql`SELECT val FROM ${sql.unsafe(tbl)} WHERE id = 1`;
      expect(final[0].val).toBe(30); // 0 + 10 + 20

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("one transaction error doesn't affect another", async () => {
      const tbl1 = uniqueTableName("txn_iso_1");
      const tbl2 = uniqueTableName("txn_iso_2");
      await sql`CREATE TABLE ${sql.unsafe(tbl1)} (id INT PRIMARY KEY)`;
      await sql`CREATE TABLE ${sql.unsafe(tbl2)} (id INT PRIMARY KEY)`;

      const txn1Promise = sql
        .transaction([
          sql`INSERT INTO ${sql.unsafe(tbl1)} VALUES (1)`,
          sql`INSERT INTO ${sql.unsafe(tbl1)} VALUES (1)`, // Duplicate - will fail
        ])
        .catch(() => "failed");

      const txn2Promise = sql.transaction([
        sql`INSERT INTO ${sql.unsafe(tbl2)} VALUES (1)`,
        sql`INSERT INTO ${sql.unsafe(tbl2)} VALUES (2)`,
      ]);

      const [r1, _r2] = await Promise.all([txn1Promise, txn2Promise]);

      expect(r1).toBe("failed");

      // tbl1 should be empty (rolled back)
      const count1 = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl1)}`;
      expect(Number(count1[0].cnt)).toBe(0);

      // tbl2 should have 2 rows (committed)
      const count2 = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl2)}`;
      expect(Number(count2[0].cnt)).toBe(2);

      await sql`DROP TABLE ${sql.unsafe(tbl1)}`;
      await sql`DROP TABLE ${sql.unsafe(tbl2)}`;
    });
  });

  // ==================== Rapid Sequential Queries ====================
  describe("Rapid Sequential Queries", () => {
    test("30 rapid sequential HTTP queries", async () => {
      for (let i = 0; i < 30; i++) {
        const result = await sql`SELECT ${i}::integer as num`;
        expect(result[0].num).toBe(i);
      }
    });

    test("each query returns fresh results (no caching)", async () => {
      const tbl = uniqueTableName("no_cache");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`;

      const r1 = await sql`SELECT val FROM ${sql.unsafe(tbl)}`;
      expect(r1[0].val).toBe(1);

      await sql`UPDATE ${sql.unsafe(tbl)} SET val = 2`;
      const r2 = await sql`SELECT val FROM ${sql.unsafe(tbl)}`;
      expect(r2[0].val).toBe(2); // Must see updated value, not cached

      await sql`UPDATE ${sql.unsafe(tbl)} SET val = 3`;
      const r3 = await sql`SELECT val FROM ${sql.unsafe(tbl)}`;
      expect(r3[0].val).toBe(3);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Multiple neon() Instances Concurrently ====================
  describe("Multiple neon() Instances", () => {
    test("different neon() instances don't share state", async () => {
      const sql1 = neon(dbUrl);
      const sql2 = neon(dbUrl);
      const sql3 = neon(dbUrl);

      const [r1, r2, r3] = await Promise.all([
        sql1`SELECT 'instance_1' as val`,
        sql2`SELECT 'instance_2' as val`,
        sql3`SELECT 'instance_3' as val`,
      ]);

      expect(r1[0].val).toBe("instance_1");
      expect(r2[0].val).toBe("instance_2");
      expect(r3[0].val).toBe("instance_3");
    });

    test("fullResults and default instances concurrent", async () => {
      const sqlDefault = neon(dbUrl);
      const sqlFull = neon(dbUrl, { fullResults: true });

      const [r1, r2] = await Promise.all([sqlDefault`SELECT 1 as val`, sqlFull`SELECT 2 as val`]);

      // Default returns array
      expect(Array.isArray(r1)).toBe(true);
      expect(r1[0].val).toBe(1);

      // Full returns object with rows
      expect(r2.rows).toBeDefined();
      expect(r2.rows[0].val).toBe(2);
    });
  });
});

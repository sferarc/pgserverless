/**
 * Query Performance Tests (20 tests)
 *
 * Tests for query performance characteristics
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Query Performance Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("perf_test");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        name TEXT,
        value INTEGER,
        data JSONB
      )
    `;

    // Seed with test data
    const rows = Array.from({ length: 1000 }, (_, i) => ({
      name: `perf_row_${i}`,
      value: i,
      data: JSON.stringify({ index: i }),
    }));

    for (let i = 0; i < 10; i++) {
      const batch = rows.slice(i * 100, (i + 1) * 100);
      await sql`
        INSERT INTO ${sql.unsafe(testTable)} (name, value, data)
        SELECT * FROM jsonb_to_recordset(${JSON.stringify(batch)}::jsonb)
        AS t(name text, value integer, data jsonb)
      `;
    }
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Single Query Latency ====================
  describe("Single Query Latency", () => {
    test("simple SELECT latency", async () => {
      const start = performance.now();
      await sql`SELECT 1`;
      const elapsed = performance.now() - start;

      expect(elapsed).toBeGreaterThan(0);
      // Log for manual review
      console.log(`Simple SELECT latency: ${elapsed.toFixed(2)}ms`);
    });

    test("parameterized query latency", async () => {
      const start = performance.now();
      await sql`SELECT ${42}::integer as num`;
      const elapsed = performance.now() - start;

      expect(elapsed).toBeGreaterThan(0);
    });

    test("table query latency", async () => {
      const start = performance.now();
      await sql`SELECT * FROM ${sql.unsafe(testTable)} LIMIT 10`;
      const elapsed = performance.now() - start;

      expect(elapsed).toBeGreaterThan(0);
    });
  });

  // ==================== Batch Query Latency ====================
  describe("Batch Query Latency", () => {
    test("transaction with multiple queries", async () => {
      const start = performance.now();
      await sql.transaction([
        sql`SELECT 1`,
        sql`SELECT 2`,
        sql`SELECT 3`,
        sql`SELECT 4`,
        sql`SELECT 5`,
      ]);
      const elapsed = performance.now() - start;

      expect(elapsed).toBeGreaterThan(0);
    });

    test("batch vs sequential comparison", async () => {
      // Sequential
      const seqStart = performance.now();
      await sql`SELECT 1`;
      await sql`SELECT 2`;
      await sql`SELECT 3`;
      const seqElapsed = performance.now() - seqStart;

      // Batch (transaction)
      const batchStart = performance.now();
      await sql.transaction([sql`SELECT 1`, sql`SELECT 2`, sql`SELECT 3`]);
      const batchElapsed = performance.now() - batchStart;

      // Both should complete
      expect(seqElapsed).toBeGreaterThan(0);
      expect(batchElapsed).toBeGreaterThan(0);
    });
  });

  // ==================== Large Result Sets ====================
  describe("Large Result Sets", () => {
    test("100 rows performance", async () => {
      const start = performance.now();
      const result = await sql`SELECT * FROM ${sql.unsafe(testTable)} LIMIT 100`;
      const _elapsed = performance.now() - start;

      expect(asRows(result).length).toBe(100);
    });

    test("1000 rows performance", async () => {
      const start = performance.now();
      const result = await sql`SELECT * FROM ${sql.unsafe(testTable)} LIMIT 1000`;
      const _elapsed = performance.now() - start;

      expect(asRows(result).length).toBe(1000);
    });

    test("generated series performance", async () => {
      const start = performance.now();
      const result = await sql`SELECT generate_series(1, 5000) as n`;
      const _elapsed = performance.now() - start;

      expect(asRows(result).length).toBe(5000);
    });
  });

  // ==================== Concurrent Queries ====================
  describe("Concurrent Queries", () => {
    test("10 concurrent queries", async () => {
      const start = performance.now();
      const queries = Array.from({ length: 10 }, (_, i) => sql`SELECT ${i} as n`);
      const results = await Promise.all(queries);
      const _elapsed = performance.now() - start;

      expect(results.length).toBe(10);
    });

    test("50 concurrent queries", async () => {
      const start = performance.now();
      const queries = Array.from({ length: 50 }, (_, i) => sql`SELECT ${i} as n`);
      const results = await Promise.all(queries);
      const _elapsed = performance.now() - start;

      expect(results.length).toBe(50);
    });

    test("Pool concurrent queries", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 10 });

      const start = performance.now();
      const queries = Array.from({ length: 20 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );
      const results = await Promise.all(queries);
      const _elapsed = performance.now() - start;

      expect(results.length).toBe(20);
      await pool.end();
    });
  });

  // ==================== Connection Performance ====================
  describe("Connection Performance", () => {
    test("HTTP connection overhead", async () => {
      // First query has connection overhead
      const coldStart = performance.now();
      const coldSql = neon(dbUrl);
      await coldSql`SELECT 1`;
      const coldElapsed = performance.now() - coldStart;

      // Subsequent query reuses connection
      const warmStart = performance.now();
      await coldSql`SELECT 2`;
      const warmElapsed = performance.now() - warmStart;

      expect(coldElapsed).toBeGreaterThan(0);
      expect(warmElapsed).toBeGreaterThan(0);
    });

    test("WebSocket connection establishment", async () => {
      const start = performance.now();
      const client = new Client(dbUrl);
      await client.connect();
      const connectElapsed = performance.now() - start;

      await client.query("SELECT 1");
      await client.end();

      expect(connectElapsed).toBeGreaterThan(0);
    });

    test("Pool connection reuse benefit", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 1 });

      // First query - connection establishment
      const first = performance.now();
      await pool.query("SELECT 1");
      const firstElapsed = performance.now() - first;

      // Second query - connection reuse
      const second = performance.now();
      await pool.query("SELECT 2");
      const secondElapsed = performance.now() - second;

      // Third query - still reusing
      const third = performance.now();
      await pool.query("SELECT 3");
      const _thirdElapsed = performance.now() - third;

      await pool.end();

      expect(firstElapsed).toBeGreaterThan(0);
      expect(secondElapsed).toBeGreaterThan(0);
    });
  });

  // ==================== Type Parsing ====================
  describe("Type Parsing Performance", () => {
    test("JSON parsing overhead", async () => {
      const start = performance.now();
      const result = await sql`
        SELECT data FROM ${sql.unsafe(testTable)} LIMIT 100
      `;
      const _elapsed = performance.now() - start;

      expect(asRows(result).length).toBe(100);
      expect(asRows(result)[0].data).toBeDefined();
    });

    test("large JSON response", async () => {
      const largeJson = JSON.stringify(
        Array.from({ length: 1000 }, (_, i) => ({
          id: i,
          name: `item_${i}`,
          nested: { value: i * 2 },
        })),
      );

      const start = performance.now();
      const result = await sql`SELECT ${largeJson}::jsonb as data`;
      const _elapsed = performance.now() - start;

      expect(asRows(result).length).toBe(1);
    });
  });

  // ==================== Query Complexity ====================
  describe("Query Complexity", () => {
    test("simple vs complex query", async () => {
      // Simple
      const simpleStart = performance.now();
      await sql`SELECT 1`;
      const simpleElapsed = performance.now() - simpleStart;

      // Complex with aggregation
      const complexStart = performance.now();
      await sql`
        SELECT
          COUNT(*) as total,
          AVG(value) as avg_value,
          MAX(value) as max_value,
          MIN(value) as min_value
        FROM ${sql.unsafe(testTable)}
      `;
      const complexElapsed = performance.now() - complexStart;

      expect(simpleElapsed).toBeGreaterThan(0);
      expect(complexElapsed).toBeGreaterThan(0);
    });

    test("join query performance", async () => {
      const start = performance.now();
      await sql`
        SELECT t1.name, t2.name as joined_name
        FROM ${sql.unsafe(testTable)} t1
        CROSS JOIN ${sql.unsafe(testTable)} t2
        LIMIT 100
      `;
      const elapsed = performance.now() - start;

      expect(elapsed).toBeGreaterThan(0);
    });
  });
});

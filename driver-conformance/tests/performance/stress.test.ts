/**
 * Stress Tests (15 tests)
 *
 * Tests for behavior under extreme conditions
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Stress Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("stress_test");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        data TEXT,
        num INTEGER
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Maximum Connections ====================
  describe("Maximum Connections", () => {
    test("pool handles many connection requests", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      // Request more connections than max
      const connectionRequests = Array.from({ length: 20 }, async () => {
        const client = await pool.connect();
        await client.query("SELECT 1");
        client.release();
        return true;
      });

      const results = await Promise.all(connectionRequests);
      expect(results.filter((r) => r).length).toBe(20);

      await pool.end();
    });

    test("handles rapid connect/disconnect cycles", async () => {
      for (let i = 0; i < 10; i++) {
        const client = new Client(dbUrl);
        await client.connect();
        await client.query("SELECT 1");
        await client.end();
      }
      expect(true).toBe(true);
    }, 30000);
  });

  // ==================== Maximum Query Size ====================
  describe("Maximum Query Size", () => {
    test("handles large query string", async () => {
      // Large string in query
      const largeString = "x".repeat(50000);
      const result = await sql`SELECT ${largeString} as data`;
      expect(asRows(result)[0].data).toBe(largeString);
    });

    test("handles many parameters", async () => {
      const params = Array.from({ length: 50 }, (_, i) => i);
      const _placeholders = params.map((_, i) => `$${i + 1}`).join(", ");

      const result = await sql.query(
        `SELECT ${params.map((_, i) => `$${i + 1}::integer`).join(" + ")} as total`,
        params,
      );

      expect(Number(asRows(result)[0].total)).toBe(params.reduce((a, b) => a + b, 0));
    });
  });

  // ==================== Maximum Result Size ====================
  describe("Maximum Result Size", () => {
    test("handles large result set (10000 rows)", async () => {
      const result = await sql`SELECT generate_series(1, 10000) as n`;
      expect(asRows(result).length).toBe(10000);
    });

    test("handles wide rows (many columns)", async () => {
      const cols = Array.from({ length: 50 }, (_, i) => `${i} as col${i}`).join(", ");
      const result = await sql.query(`SELECT ${cols}`, []);
      expect(Object.keys(asRows(result)[0]).length).toBe(50);
    });

    test("handles large text in result", async () => {
      const size = 100000;
      const result = await sql`SELECT repeat('x', ${size}) as data`;
      expect((asRows(result)[0].data as string).length).toBe(size);
    });
  });

  // ==================== Maximum Parameter Count ====================
  describe("Maximum Parameter Count", () => {
    test("handles 100 parameters", async () => {
      const count = 100;
      const params = Array.from({ length: count }, (_, i) => i);
      // Add column aliases so each parameter gets a named column
      const placeholders = params.map((_, i) => `$${i + 1}::integer as col${i}`).join(", ");

      const result = await sql.query(`SELECT ${placeholders}`, params);
      expect(Object.keys(asRows(result)[0]).length).toBe(count);
    });
  });

  // ==================== Long-Running Queries ====================
  describe("Long-Running Queries", () => {
    test("handles moderately long query", async () => {
      // pg_sleep for a short time
      const result = await sql`SELECT pg_sleep(0.5), 'done' as status`;
      expect(asRows(result)[0].status).toBe("done");
    });
  });

  // ==================== Many Small Queries ====================
  describe("Many Small Queries", () => {
    test("handles 200 sequential queries", async () => {
      for (let i = 0; i < 200; i++) {
        await sql`SELECT ${i} as n`;
      }
      expect(true).toBe(true);
    }, 60000);

    test("handles 100 concurrent queries", async () => {
      const queries = Array.from({ length: 100 }, (_, i) => sql`SELECT ${i} as n`);
      const results = await Promise.all(queries);
      expect(results.length).toBe(100);
    });
  });

  // ==================== Mixed Workload ====================
  describe("Mixed Workload", () => {
    test("handles mixed read/write workload", async () => {
      const ops = [];

      // Reads
      for (let i = 0; i < 10; i++) {
        ops.push(sql`SELECT * FROM ${sql.unsafe(testTable)} LIMIT 10`);
      }

      // Writes
      for (let i = 0; i < 5; i++) {
        ops.push(
          sql`INSERT INTO ${sql.unsafe(testTable)} (data, num) VALUES (${`stress_${i}`}, ${i}) RETURNING id`,
        );
      }

      // More reads
      for (let i = 0; i < 5; i++) {
        ops.push(sql`SELECT COUNT(*) FROM ${sql.unsafe(testTable)}`);
      }

      const results = await Promise.all(ops);
      expect(results.length).toBe(20);
    });

    test("handles interleaved transactions", async () => {
      const txns = Array.from({ length: 5 }, (_, i) =>
        sql.transaction([sql`SELECT ${i} as txn_id`, sql`SELECT ${i * 2} as doubled`]),
      );

      const results = await Promise.all(txns);
      expect(results.length).toBe(5);
    });
  });

  // ==================== Data Consistency ====================
  describe("Data Consistency", () => {
    test("maintains consistency under concurrent writes", async () => {
      const name = uniqueTableName("consistency");

      // Concurrent inserts
      const inserts = Array.from(
        { length: 20 },
        (_, i) =>
          sql`INSERT INTO ${sql.unsafe(testTable)} (data, num) VALUES (${`${name}_${i}`}, ${i})`,
      );

      await Promise.all(inserts);

      // Verify count
      const result =
        await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)} WHERE data LIKE ${`${name}%`}`;
      expect(Number(asRows(result)[0].cnt)).toBe(20);
    });

    test("transaction atomicity under stress", async () => {
      const name = uniqueTableName("atomic");

      // Some transactions will succeed, some will fail
      const txns = Array.from({ length: 10 }, (_, i) =>
        sql
          .transaction([
            sql`INSERT INTO ${sql.unsafe(testTable)} (data, num) VALUES (${`${name}_${i}`}, ${i})`,
            i % 3 === 0
              ? sql`SELECT 1/0` // Will fail every 3rd transaction
              : sql`SELECT 1`,
          ])
          .catch(() => null),
      );

      await Promise.all(txns);

      // Only successful transactions should have committed
      const result =
        await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)} WHERE data LIKE ${`${name}%`}`;
      // Failed transactions should not have committed
      expect(Number(asRows(result)[0].cnt)).toBeLessThan(10);
    });
  });

  // ==================== Concurrent Writes ====================
  describe("Concurrent Writes", () => {
    test("handles concurrent inserts to same table", async () => {
      const name = uniqueTableName("concurrent_ins");

      const inserts = Array.from(
        { length: 30 },
        (_, i) =>
          sql`INSERT INTO ${sql.unsafe(testTable)} (data, num) VALUES (${`${name}_${i}`}, ${i})`,
      );

      await Promise.all(inserts);

      const count =
        await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)} WHERE data LIKE ${`${name}%`}`;
      expect(Number(asRows(count)[0].cnt)).toBe(30);
    });

    test("handles concurrent updates", async () => {
      const name = uniqueTableName("concurrent_upd");

      // First insert some rows
      for (let i = 0; i < 10; i++) {
        await sql`INSERT INTO ${sql.unsafe(testTable)} (data, num) VALUES (${`${name}_${i}`}, 0)`;
      }

      // Concurrent updates
      const updates = Array.from(
        { length: 10 },
        (_, i) =>
          sql`UPDATE ${sql.unsafe(testTable)} SET num = num + 1 WHERE data = ${`${name}_${i}`}`,
      );

      await Promise.all(updates);

      const result =
        await sql`SELECT SUM(num) as total FROM ${sql.unsafe(testTable)} WHERE data LIKE ${`${name}%`}`;
      expect(Number(asRows(result)[0].total)).toBe(10);
    });
  });
});

/**
 * Node.js Runtime Tests (20 tests)
 *
 * Tests for Node.js-specific functionality with the Neon serverless driver
 */

import { Client, neon, neonConfig, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import ws from "ws";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Node.js Runtime Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("node_test");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        data TEXT,
        created_at TIMESTAMPTZ DEFAULT NOW()
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Basic Connectivity ====================
  describe("Basic Connectivity", () => {
    test("basic connectivity via HTTP", async () => {
      const result = await sql`SELECT 1 as num`;
      expect(result).toEqual([{ num: 1 }]);
    });

    test("basic connectivity via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 1 as num");
      expect(result.rows[0].num).toBe(1);
      await client.end();
    });

    test("WebSocket with ws package", async () => {
      // ws package should be properly configured
      expect(neonConfig.webSocketConstructor).toBe(ws);

      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 'ws package' as source");
      expect(result.rows[0].source).toBe("ws package");
      await client.end();
    });

    test("HTTP fetch (native)", async () => {
      // Node.js 18+ has native fetch
      const result = await sql`SELECT 'native fetch' as method`;
      expect(asRows(result)[0].method).toBe("native fetch");
    });
  });

  // ==================== Async/Await & Promises ====================
  describe("Async/Await & Promises", () => {
    test("async/await pattern", async () => {
      const result = await sql`SELECT 1 as num`;
      expect(asRows(result)[0].num).toBe(1);
    });

    test("promise chaining", async () => {
      const result = await sql`SELECT 1 as num`
        .then((rows) => asRows(rows)[0])
        .then((row) => row.num);

      expect(result).toBe(1);
    });

    test("Promise.all for concurrent queries", async () => {
      const results = await Promise.all([
        sql`SELECT 1 as n`,
        sql`SELECT 2 as n`,
        sql`SELECT 3 as n`,
      ]);

      expect(results.length).toBe(3);
      expect(asRows(results[0])[0].n).toBe(1);
      expect(asRows(results[1])[0].n).toBe(2);
      expect(asRows(results[2])[0].n).toBe(3);
    });

    test("Promise.race", async () => {
      const result = await Promise.race([sql`SELECT 1 as n`, sql`SELECT 2 as n`]);

      expect([1, 2]).toContain(asRows(result)[0].n as number);
    });

    test("Promise.allSettled", async () => {
      const results = await Promise.allSettled([
        sql`SELECT 1 as n`,
        sql`SELECT 1/0 as n`, // This will error
        sql`SELECT 3 as n`,
      ]);

      expect(results[0].status).toBe("fulfilled");
      expect(results[1].status).toBe("rejected");
      expect(results[2].status).toBe("fulfilled");
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("try/catch error handling", async () => {
      try {
        await sql`SELECT * FROM nonexistent_table_xyz`;
        expect(true).toBe(false);
      } catch (e: unknown) {
        expect(e).toBeDefined();
        expect((e as { code: string }).code).toBe("42P01");
      }
    });

    test("promise rejection handling", async () => {
      const result = await sql`INVALID SYNTAX`.catch((e) => ({
        error: true,
        code: e.code,
      }));

      expect(result).toHaveProperty("error", true);
    });

    test("error recovery - continue after error", async () => {
      try {
        await sql`INVALID`;
      } catch {
        // Expected
      }

      const result = await sql`SELECT 1 as num`;
      expect(asRows(result)[0].num).toBe(1);
    });
  });

  // ==================== Memory & Performance ====================
  describe("Memory & Performance", () => {
    test("handles large result set", async () => {
      const result = await sql`SELECT generate_series(1, 10000) as n`;
      expect(asRows(result).length).toBe(10000);
    });

    test("handles many small queries", async () => {
      const queries = Array.from({ length: 100 }, (_, i) => sql`SELECT ${i} as n`);
      const results = await Promise.all(queries);
      expect(results.length).toBe(100);
    });

    test("handles large text data", async () => {
      const largeText = "x".repeat(100000);
      const result = await sql`SELECT ${largeText} as data`;
      expect(asRows(result)[0].data).toBe(largeText);
    });

    test("handles binary data", async () => {
      const buffer = Buffer.alloc(10000, 0xab);
      const result = await sql`SELECT ${buffer}::bytea as data`;
      expect(asRows(result).length).toBe(1);
    });
  });

  // ==================== Concurrency ====================
  describe("Concurrency", () => {
    test("concurrent HTTP queries", async () => {
      const queries = Array.from({ length: 50 }, (_, i) => sql`SELECT ${i} as n`);
      const results = await Promise.all(queries);
      expect(results.length).toBe(50);
    });

    test("concurrent WebSocket queries", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      const queries = Array.from({ length: 20 }, (_, i) =>
        pool.query("SELECT $1::integer as n", [i]),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(20);

      await pool.end();
    });

    test("Pool handles concurrent connections", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 10 });

      const clients = await Promise.all(Array.from({ length: 5 }, () => pool.connect()));

      expect(clients.length).toBe(5);

      await Promise.all(
        clients.map((c) => {
          c.release();
          return Promise.resolve();
        }),
      );
      await pool.end();
    });
  });

  // ==================== TypeScript Integration ====================
  describe("TypeScript Integration", () => {
    test("type inference from query", async () => {
      const result = await sql`SELECT 1 as num, 'text' as str`;
      // TypeScript should infer the type
      const row = asRows(result)[0];
      expect(row.num).toBe(1);
      expect(row.str).toBe("text");
    });

    test("generic type parameters", async () => {
      const result = await neon(dbUrl, { fullResults: true })`SELECT 1 as num`;
      expect(result.rows).toBeDefined();
      expect(result.fields).toBeDefined();
    });
  });

  // ==================== Module Systems ====================
  describe("Module Systems", () => {
    test("ESM import works", () => {
      // This file uses ESM imports
      expect(neon).toBeDefined();
      expect(Client).toBeDefined();
      expect(Pool).toBeDefined();
    });

    test("neonConfig is accessible", () => {
      expect(neonConfig).toBeDefined();
      expect(neonConfig.defaults).toBeDefined();
    });
  });

  // ==================== Environment Variables ====================
  describe("Environment Variables", () => {
    test("reads database URL from env", () => {
      const url = getDatabaseUrl();
      expect(url).toBeDefined();
      expect(url.length).toBeGreaterThan(0);
    });

    test("connection works with env-based URL", async () => {
      const result = await sql`SELECT 'env test' as source`;
      expect(asRows(result)[0].source).toBe("env test");
    });
  });
});

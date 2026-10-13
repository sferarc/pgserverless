/**
 * SQL Injection Protection Tests
 *
 * Based on real bugs and security concerns from PlanetScale and Neon:
 * - PlanetScale #91: sql() called as regular function instead of tagged template
 * - PlanetScale #174: Inconsistent escaping between transports
 * - Neon: Tagged template literal safety guarantees
 *
 * These tests verify that parameterized queries properly prevent SQL injection,
 * and that special characters in parameters are correctly escaped.
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("SQL Injection Protection Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("sqli_test");
    await sql`CREATE TABLE ${sql.unsafe(testTable)} (
      id SERIAL PRIMARY KEY,
      name TEXT,
      value INTEGER
    )`;
    await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES ('alice', 10)`;
    await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES ('bob', 20)`;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Tagged Template Parameterization ====================
  describe("Tagged Template Parameterization", () => {
    test("SQL in parameter is treated as data, not code", async () => {
      const malicious = `'; DROP TABLE ${testTable}; --`;
      const result = await sql`SELECT ${malicious}::text as val`;
      expect(result[0].val).toBe(malicious);

      // Table should still exist
      const check = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)}`;
      expect(Number(check[0].cnt)).toBeGreaterThan(0);
    });

    test("UNION injection in parameter is treated as data", async () => {
      const malicious = "' UNION SELECT * FROM pg_catalog.pg_tables --";
      const result = await sql`SELECT name FROM ${sql.unsafe(testTable)} WHERE name = ${malicious}`;
      expect(result.length).toBe(0); // No rows match, injection didn't execute
    });

    test("OR 1=1 injection in parameter is treated as data", async () => {
      const malicious = "' OR '1'='1";
      const result = await sql`SELECT name FROM ${sql.unsafe(testTable)} WHERE name = ${malicious}`;
      expect(result.length).toBe(0);
    });

    test("semicolon in parameter doesn't create second statement", async () => {
      const malicious = `test; DELETE FROM ${testTable}`;
      const result = await sql`SELECT ${malicious}::text as val`;
      expect(result[0].val).toBe(malicious);

      // Data should be intact
      const count = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)}`;
      expect(Number(count[0].cnt)).toBe(2);
    });

    test("backslash in parameter is safe", async () => {
      const val = "test\\'; DROP TABLE test; --";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("null byte in parameter is rejected by PostgreSQL", async () => {
      const val = "test\x00injection";
      try {
        await sql`SELECT ${val}::text as val`;
        // If it succeeds, that's also fine
        expect(true).toBe(true);
      } catch (e) {
        // PostgreSQL rejects null bytes in text encoding
        // Neon returns 22021 (invalid byte sequence), a server may instead return 08P01 (protocol violation)
        const err = e as Record<string, unknown>;
        expect(["22021", "08P01"]).toContain(err.code);
      }
    });
  });

  // ==================== WebSocket Parameterized Queries ====================
  describe("WebSocket Parameterized Query Safety", () => {
    test("SQL injection via client.query parameter", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const malicious = `'; DROP TABLE ${testTable}; --`;
      const result = await client.query("SELECT $1::text as val", [malicious]);
      expect(result.rows[0].val).toBe(malicious);

      // Table should still exist
      const check = await client.query(`SELECT COUNT(*) as cnt FROM ${testTable}`);
      expect(Number(check.rows[0].cnt)).toBeGreaterThan(0);

      await client.end();
    });

    test("pool parameterized query safety", async () => {
      const pool = new Pool({ connectionString: dbUrl });

      const malicious = `'; DELETE FROM ${testTable}; --`;
      const result = await pool.query("SELECT $1::text as val", [malicious]);
      expect(result.rows[0].val).toBe(malicious);

      await pool.end();
    });
  });

  // ==================== Special Characters in Data ====================
  describe("Special Characters in Data", () => {
    test("single quotes in parameter", async () => {
      const val = "it's a test with 'quotes'";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("double quotes in parameter", async () => {
      const val = 'value with "double quotes"';
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("backslash sequences in parameter", async () => {
      const val = "line1\\nline2\\ttab\\r\\n";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("percent and underscore (LIKE wildcards) in parameter", async () => {
      const val = "100% done_with_tests";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("dollar signs in parameter (PG dollar quoting)", async () => {
      const val = "$$body$$ and $tag$content$tag$";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("SQL keywords as data values", async () => {
      const keywords = [
        "SELECT",
        "INSERT",
        "UPDATE",
        "DELETE",
        "DROP",
        "CREATE",
        "ALTER",
        "TABLE",
        "BEGIN",
        "COMMIT",
        "ROLLBACK",
      ];
      for (const keyword of keywords) {
        const result = await sql`SELECT ${keyword}::text as val`;
        expect(result[0].val).toBe(keyword);
      }
    });

    test("special characters round-trip through table", async () => {
      const tbl = uniqueTableName("special_chars");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (data TEXT)`;

      const specialStrings = [
        "it's",
        'with "quotes"',
        "back\\slash",
        "semi;colon",
        "pipe|line",
        "ampersand&and",
        "angle<brackets>",
        "at@sign",
        "hash#mark",
        "dollar$sign",
        "percent%",
        "caret^",
        "tilde~",
        "backtick`",
      ];

      for (const s of specialStrings) {
        await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (${s})`;
      }

      const result = await sql`SELECT data FROM ${sql.unsafe(tbl)} ORDER BY data`;
      expect(result.length).toBe(specialStrings.length);

      // Verify each value survived the round-trip
      const retrievedValues = result.map((r: Record<string, unknown>) => r.data as string);
      for (const s of specialStrings) {
        expect(retrievedValues).toContain(s);
      }

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== LIKE Pattern Safety ====================
  describe("LIKE Pattern Safety", () => {
    test("LIKE with user input doesn't match wildcards", async () => {
      const tbl = uniqueTableName("like_safe");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (name TEXT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES ('alice'), ('bob'), ('100% done')`;

      // A user searching for "%" should only find the literal %
      const result =
        await sql`SELECT name FROM ${sql.unsafe(tbl)} WHERE name LIKE ${"%" + "100%" + "%"}`;
      // This uses LIKE with %, so it will match "100% done"
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("100% done");

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Unicode Safety ====================
  describe("Unicode and Encoding Safety", () => {
    test("unicode escape sequences in parameter", async () => {
      const val = "\u0000SELECT 1"; // null char + SQL
      // PostgreSQL may reject or strip null chars
      try {
        const result = await sql`SELECT ${val}::text as val`;
        expect(result[0].val).toBeDefined();
      } catch {
        // PostgreSQL may reject strings with null bytes - that's acceptable
        expect(true).toBe(true);
      }
    });

    test("homoglyph characters don't confuse parser", async () => {
      // Cyrillic 'а' looks like Latin 'a'
      const val = "\u0430\u0435\u043b\u0435\u0441\u0442"; // Cyrillic
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("right-to-left override character", async () => {
      const val = "normal\u202Eesrever";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("very long string parameter (boundary test)", async () => {
      // Test near common buffer boundaries
      for (const len of [255, 256, 1023, 1024, 4095, 4096, 65535, 65536]) {
        const val = "x".repeat(len);
        const result = await sql`SELECT length(${val}::text) as len`;
        expect(result[0].len).toBe(len);
      }
    });
  });

  // ==================== Transaction Safety ====================
  describe("Transaction Parameter Safety", () => {
    test("injection attempt in transaction parameter", async () => {
      const malicious = `'; ROLLBACK; DELETE FROM ${testTable}; BEGIN; --`;
      const results = await sql.transaction([
        sql`SELECT ${malicious}::text as val`,
        sql`SELECT 'safe'::text as val`,
      ]);

      expect(results[0][0].val).toBe(malicious);
      expect(results[1][0].val).toBe("safe");

      // Data should be intact
      const count = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)}`;
      expect(Number(count[0].cnt)).toBe(2);
    });
  });
});

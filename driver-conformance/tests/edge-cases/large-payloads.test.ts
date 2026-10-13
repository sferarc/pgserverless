/**
 * Large Payload and Response Tests
 *
 * Based on real bugs from PlanetScale and Neon:
 * - PlanetScale #97: Large response bodies cause HTTP 500
 * - PlanetScale #108: Responses >1MB silently truncated
 * - Neon #118: Buffer values JSON.stringify()'d instead of hex-encoded
 * - General: Proxy response size limits, chunked transfer issues
 *
 * These tests verify that large queries, responses, and parameters
 * work correctly across both HTTP and WebSocket transports.
 */

import { Client, neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Large Payload and Response Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("large_payload");
    await sql`CREATE TABLE ${sql.unsafe(testTable)} (
      id SERIAL PRIMARY KEY,
      data TEXT,
      label TEXT
    )`;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Large Row Counts ====================
  describe("Large Row Counts", () => {
    test("500 rows via HTTP", async () => {
      const result = await sql`SELECT generate_series(1, 500) as num`;
      expect(result.length).toBe(500);
      expect(result[0].num).toBe(1);
      expect(result[499].num).toBe(500);
    });

    test("1000 rows via HTTP", async () => {
      const result = await sql`SELECT generate_series(1, 1000) as num`;
      expect(result.length).toBe(1000);
    });

    test("5000 rows via HTTP", async () => {
      const result = await sql`SELECT generate_series(1, 5000) as num`;
      expect(result.length).toBe(5000);
    });

    test("500 rows via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT generate_series(1, 500) as num");
      expect(result.rows.length).toBe(500);
      await client.end();
    });

    test("1000 rows via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT generate_series(1, 1000) as num");
      expect(result.rows.length).toBe(1000);
      await client.end();
    });
  });

  // ==================== Large Text Values ====================
  describe("Large Text Values", () => {
    test("1KB text value via HTTP", async () => {
      const text = "a".repeat(1024);
      const result = await sql`SELECT ${text}::text as val`;
      expect(result[0].val.length).toBe(1024);
    });

    test("10KB text value via HTTP", async () => {
      const text = "b".repeat(10240);
      const result = await sql`SELECT ${text}::text as val`;
      expect(result[0].val.length).toBe(10240);
    });

    test("100KB text value via HTTP", async () => {
      const text = "c".repeat(102400);
      const result = await sql`SELECT ${text}::text as val`;
      expect(result[0].val.length).toBe(102400);
    });

    test("1MB text value via HTTP", async () => {
      const text = "d".repeat(1024 * 1024);
      const result = await sql`SELECT ${text}::text as val`;
      expect(result[0].val.length).toBe(1024 * 1024);
    });

    test("10KB text value via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const text = "e".repeat(10240);
      const result = await client.query("SELECT $1::text as val", [text]);
      expect(result.rows[0].val.length).toBe(10240);
      await client.end();
    });

    test("100KB text value via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const text = "f".repeat(102400);
      const result = await client.query("SELECT $1::text as val", [text]);
      expect(result.rows[0].val.length).toBe(102400);
      await client.end();
    });

    test("large text round-trip through table", async () => {
      const text = "g".repeat(50000);
      const label = uniqueTableName("large_text");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${text}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;
      expect(result[0].data.length).toBe(50000);
      expect(result[0].data).toBe(text);
    });
  });

  // ==================== Large JSON Payloads ====================
  describe("Large JSON Payloads", () => {
    test("JSON object with many keys", async () => {
      const obj: Record<string, number> = {};
      for (let i = 0; i < 500; i++) {
        obj[`key_${i}`] = i;
      }
      const result = await sql`SELECT ${JSON.stringify(obj)}::jsonb as val`;
      expect(Object.keys(result[0].val as object).length).toBe(500);
    });

    test("large JSON array", async () => {
      const arr = Array.from({ length: 1000 }, (_, i) => ({
        id: i,
        name: `item_${i}`,
      }));
      const result = await sql`SELECT ${JSON.stringify(arr)}::jsonb as val`;
      expect((result[0].val as unknown[]).length).toBe(1000);
    });

    test("deeply nested JSON (100 levels)", async () => {
      let nested: Record<string, unknown> = { value: "deep" };
      for (let i = 0; i < 100; i++) {
        nested = { level: i, child: nested };
      }
      const result = await sql`SELECT ${JSON.stringify(nested)}::jsonb as val`;
      expect(result[0].val).toBeDefined();
      // Verify structure by traversing
      let current = result[0].val as Record<string, unknown>;
      for (let i = 99; i >= 0; i--) {
        expect(current.level).toBe(i);
        current = current.child as Record<string, unknown>;
      }
      expect(current.value).toBe("deep");
    });

    test("JSON with large string values", async () => {
      const obj = {
        large_field: "x".repeat(10000),
        another: "y".repeat(10000),
      };
      const result = await sql`SELECT ${JSON.stringify(obj)}::jsonb as val`;
      const parsed = result[0].val as Record<string, string>;
      expect(parsed.large_field.length).toBe(10000);
      expect(parsed.another.length).toBe(10000);
    });
  });

  // ==================== Many Columns ====================
  describe("Many Columns", () => {
    test("50 columns in single row", async () => {
      const cols = Array.from({ length: 50 }, (_, i) => `${i}::integer as col_${i}`).join(", ");
      const result = await sql.query(`SELECT ${cols}`, []);
      expect(Object.keys(result[0]).length).toBe(50);
      for (let i = 0; i < 50; i++) {
        expect(result[0][`col_${i}`]).toBe(i);
      }
    });

    test("100 columns in single row", async () => {
      const cols = Array.from({ length: 100 }, (_, i) => `${i}::integer as col_${i}`).join(", ");
      const result = await sql.query(`SELECT ${cols}`, []);
      expect(Object.keys(result[0]).length).toBe(100);
    });

    test("many columns with mixed types", async () => {
      const intCols = Array.from({ length: 20 }, (_, i) => `${i}::integer as int_${i}`);
      const textCols = Array.from({ length: 20 }, (_, i) => `'text_${i}'::text as text_${i}`);
      const boolCols = Array.from({ length: 10 }, (_, i) => `${i % 2 === 0}::boolean as bool_${i}`);
      const allCols = [...intCols, ...textCols, ...boolCols].join(", ");
      const result = await sql.query(`SELECT ${allCols}`, []);
      expect(Object.keys(result[0]).length).toBe(50);
    });
  });

  // ==================== Many Rows with Wide Data ====================
  describe("Many Rows with Wide Data", () => {
    test("100 rows each with 10 columns via HTTP", async () => {
      const result = await sql`
        SELECT
          gs as id,
          gs * 2 as doubled,
          gs * 3 as tripled,
          (gs % 2 = 0) as is_even,
          'row_' || gs::text as label,
          gs::float8 / 3.0 as ratio,
          ARRAY[gs, gs+1, gs+2] as arr,
          ('{"n":' || gs::text || '}')::jsonb as json_val,
          gs::text as text_val,
          gs::bigint as big_val
        FROM generate_series(1, 100) gs
      `;
      expect(result.length).toBe(100);
      expect(result[0].id).toBe(1);
      expect(result[99].id).toBe(100);
    });

    test("batch insert and select many rows", async () => {
      const tbl = uniqueTableName("batch_large");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT, val TEXT)`;

      // Insert 200 rows using generate_series for efficiency
      await sql`INSERT INTO ${sql.unsafe(tbl)} (id, val)
        SELECT gs, 'val_' || gs::text
        FROM generate_series(0, 199) gs`;

      const result = await sql`SELECT * FROM ${sql.unsafe(tbl)} ORDER BY id`;
      expect(result.length).toBe(200);
      expect(result[0].id).toBe(0);
      expect(result[0].val).toBe("val_0");
      expect(result[199].id).toBe(199);
      expect(result[199].val).toBe("val_199");

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Large Parameters ====================
  describe("Large Parameters", () => {
    test("many parameters in single query via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const params = Array.from({ length: 50 }, (_, i) => i);
      const placeholders = params.map((_, i) => `$${i + 1}::integer`).join(" + ");
      const result = await client.query(`SELECT ${placeholders} as total`, params);
      // Sum of 0..49 = 1225
      expect(result.rows[0].total).toBe(1225);

      await client.end();
    });

    test("large text parameter via WebSocket", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const largeText = "z".repeat(100000);
      const result = await client.query("SELECT length($1::text) as len", [largeText]);
      expect(result.rows[0].len).toBe(100000);

      await client.end();
    });
  });

  // ==================== HTTP vs WebSocket Consistency for Large Data ====================
  describe("Large Data Cross-Transport Consistency", () => {
    test("same large result via HTTP and WebSocket", async () => {
      const httpResult = await sql`
        SELECT gs::integer as num, 'item_' || gs::text as label
        FROM generate_series(1, 200) gs
      `;

      const client = new Client(dbUrl);
      await client.connect();
      const wsResult = await client.query(
        "SELECT gs::integer as num, 'item_' || gs::text as label FROM generate_series(1, 200) gs",
      );
      await client.end();

      expect(httpResult.length).toBe(200);
      expect(wsResult.rows.length).toBe(200);

      for (let i = 0; i < 200; i++) {
        expect(httpResult[i].num).toBe(wsResult.rows[i].num);
        expect(httpResult[i].label).toBe(wsResult.rows[i].label);
      }
    });
  });
});

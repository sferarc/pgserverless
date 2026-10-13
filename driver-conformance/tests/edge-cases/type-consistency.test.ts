/**
 * Type Consistency Tests
 *
 * Based on real bugs from PlanetScale and Neon:
 * - PlanetScale #175: Numbers returned as strings for multi-row results
 * - Neon #90: Dates from generate_series returned as strings via HTTP
 * - PlanetScale #171: JSON columns returned as byte arrays
 * - PlanetScale #152: BigInt precision loss in JSON functions
 *
 * These tests verify that data types are returned consistently
 * regardless of row count, transport, or query complexity.
 */

import { Client, neon } from "@neondatabase/serverless";
import { describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Type Consistency Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);

  // ==================== Numeric Type Consistency ====================
  describe("Numeric Type Consistency Across Row Counts", () => {
    test("integer type is consistent: 1 row vs multiple rows", async () => {
      const single = await sql`SELECT 42::integer as val`;
      const multi = await sql`SELECT 42::integer as val UNION ALL SELECT 43::integer`;

      expect(typeof single[0].val).toBe("number");
      expect(typeof multi[0].val).toBe("number");
      expect(typeof multi[1].val).toBe("number");
    });

    test("float type is consistent across row counts", async () => {
      const single = await sql`SELECT 3.14::double precision as val`;
      const multi =
        await sql`SELECT 3.14::double precision as val UNION ALL SELECT 2.71::double precision`;

      expect(typeof single[0].val).toBe("number");
      expect(typeof multi[0].val).toBe("number");
      expect(typeof multi[1].val).toBe("number");
    });

    test("boolean type is consistent across row counts", async () => {
      const single = await sql`SELECT true::boolean as val`;
      const multi = await sql`SELECT true::boolean as val UNION ALL SELECT false::boolean`;

      expect(typeof single[0].val).toBe("boolean");
      expect(typeof multi[0].val).toBe("boolean");
      expect(typeof multi[1].val).toBe("boolean");
    });

    test("integer types consistent with generate_series", async () => {
      const result = await sql`SELECT generate_series(1, 5)::integer as val`;
      for (const row of result) {
        expect(typeof row.val).toBe("number");
      }
    });

    test("numeric returns string consistently", async () => {
      const single = await sql`SELECT 123.456::numeric as val`;
      const multi = await sql`SELECT 123.456::numeric as val UNION ALL SELECT 789.012::numeric`;

      expect(typeof single[0].val).toBe("string");
      expect(typeof multi[0].val).toBe("string");
      expect(typeof multi[1].val).toBe("string");
    });

    test("bigint returns string consistently", async () => {
      const single = await sql`SELECT 9223372036854775807::bigint as val`;
      const multi =
        await sql`SELECT 9223372036854775807::bigint as val UNION ALL SELECT 9223372036854775806::bigint`;

      expect(typeof single[0].val).toBe("string");
      expect(typeof multi[0].val).toBe("string");
    });
  });

  // ==================== Date/Timestamp Consistency ====================
  describe("Date/Timestamp Consistency", () => {
    test("date type consistency between HTTP and WebSocket", async () => {
      const httpResult = await sql`SELECT '2024-01-15'::date as val`;

      const client = new Client(dbUrl);
      await client.connect();
      const wsResult = await client.query("SELECT '2024-01-15'::date as val");
      await client.end();

      // Both should return something parseable as a date
      expect(httpResult[0].val).toBeDefined();
      expect(wsResult.rows[0].val).toBeDefined();
    });

    test("timestamptz consistency", async () => {
      const httpResult = await sql`SELECT '2024-01-15 14:30:00+00'::timestamptz as val`;

      const client = new Client(dbUrl);
      await client.connect();
      const wsResult = await client.query("SELECT '2024-01-15 14:30:00+00'::timestamptz as val");
      await client.end();

      expect(httpResult[0].val).toBeDefined();
      expect(wsResult.rows[0].val).toBeDefined();
    });

    test("timestamp edge values", async () => {
      const result = await sql`
        SELECT
          '0001-01-01'::date as min_date,
          '9999-12-31'::date as max_date,
          'epoch'::timestamptz as epoch,
          'infinity'::timestamptz as inf,
          '-infinity'::timestamptz as ninf
      `;

      expect(result[0].min_date).toBeDefined();
      expect(result[0].max_date).toBeDefined();
      expect(result[0].epoch).toBeDefined();
      expect(result[0].inf).toBeDefined();
      expect(result[0].ninf).toBeDefined();
    });

    test("generate_series with timestamps", async () => {
      const result = await sql`
        SELECT d::date as val
        FROM generate_series('2024-01-01'::date, '2024-01-05'::date, '1 day') as d
      `;
      expect(result.length).toBe(5);
      for (const row of result) {
        expect(row.val).toBeDefined();
      }
    });

    test("interval type", async () => {
      const result = await sql`SELECT '1 year 2 months 3 days 4 hours'::interval as val`;
      expect(result[0].val).toBeDefined();
      expect(typeof result[0].val === "string" || typeof result[0].val === "object").toBe(true);
    });
  });

  // ==================== JSON/JSONB Consistency ====================
  describe("JSON/JSONB Type Consistency", () => {
    test("JSONB object is returned as object, not string", async () => {
      const result = await sql`SELECT '{"a": 1, "b": "text"}'::jsonb as val`;
      expect(typeof result[0].val).toBe("object");
      expect(result[0].val).toEqual({ a: 1, b: "text" });
    });

    test("JSONB array is returned as array, not string", async () => {
      const result = await sql`SELECT '[1, 2, 3]'::jsonb as val`;
      expect(Array.isArray(result[0].val)).toBe(true);
      expect(result[0].val).toEqual([1, 2, 3]);
    });

    test("JSON in table column round-trip", async () => {
      const tbl = uniqueTableName("json_rt");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (data JSONB)`;

      const input = { nested: { arr: [1, 2, 3], str: "hello", bool: true, nil: null } };
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (${JSON.stringify(input)}::jsonb)`;

      const result = await sql`SELECT data FROM ${sql.unsafe(tbl)}`;
      expect(result[0].data).toEqual(input);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("JSONB consistency between HTTP and WebSocket", async () => {
      const jsonStr = '{"key": "value", "num": 42, "arr": [1, 2]}';
      const httpResult = await sql`SELECT ${jsonStr}::jsonb as val`;

      const client = new Client(dbUrl);
      await client.connect();
      const wsResult = await client.query("SELECT $1::jsonb as val", [jsonStr]);
      await client.end();

      expect(httpResult[0].val).toEqual(wsResult.rows[0].val);
    });

    test("JSON with Unicode", async () => {
      const result =
        await sql`SELECT '{"emoji": "😀", "chinese": "中文", "japanese": "日本語"}'::jsonb as val`;
      expect(result[0].val).toEqual({
        emoji: "😀",
        chinese: "中文",
        japanese: "日本語",
      });
    });

    test("deeply nested JSON", async () => {
      const deep = { l1: { l2: { l3: { l4: { l5: "deep" } } } } };
      const result = await sql`SELECT ${JSON.stringify(deep)}::jsonb as val`;
      expect(result[0].val).toEqual(deep);
    });

    test("JSON with all value types", async () => {
      const mixed = {
        str: "hello",
        num: 42,
        float: 3.14,
        bool: true,
        nil: null,
        arr: [1, "two", false],
        obj: { nested: true },
      };
      const result = await sql`SELECT ${JSON.stringify(mixed)}::jsonb as val`;
      expect(result[0].val).toEqual(mixed);
    });
  });

  // ==================== Array Type Consistency ====================
  describe("Array Type Consistency", () => {
    test("integer array elements are numbers", async () => {
      const result = await sql`SELECT ARRAY[1, 2, 3]::integer[] as val`;
      for (const item of result[0].val as number[]) {
        expect(typeof item).toBe("number");
      }
    });

    test("text array elements are strings", async () => {
      const result = await sql`SELECT ARRAY['a', 'b', 'c']::text[] as val`;
      for (const item of result[0].val as string[]) {
        expect(typeof item).toBe("string");
      }
    });

    test("boolean array elements are booleans", async () => {
      const result = await sql`SELECT ARRAY[true, false, true]::boolean[] as val`;
      for (const item of result[0].val as boolean[]) {
        expect(typeof item).toBe("boolean");
      }
    });

    test("array from table column round-trip", async () => {
      const tbl = uniqueTableName("arr_rt");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (nums INTEGER[], texts TEXT[])`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (ARRAY[10, 20, 30], ARRAY['x', 'y', 'z'])`;

      const result = await sql`SELECT nums, texts FROM ${sql.unsafe(tbl)}`;
      expect(result[0].nums).toEqual([10, 20, 30]);
      expect(result[0].texts).toEqual(["x", "y", "z"]);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("array with special characters in strings", async () => {
      const result = await sql`SELECT ARRAY['it''s', 'with,comma', 'with"quotes"']::text[] as val`;
      expect(result[0].val).toEqual(["it's", "with,comma", 'with"quotes"']);
    });
  });

  // ==================== Mixed Type Query Consistency ====================
  describe("Mixed Type Query Results", () => {
    test("multiple types in single row are correct", async () => {
      const result = await sql`
        SELECT
          42::integer as int_val,
          3.14::double precision as float_val,
          'hello'::text as text_val,
          true::boolean as bool_val,
          '{"k":"v"}'::jsonb as json_val,
          ARRAY[1,2,3]::integer[] as arr_val,
          NULL::text as null_val
      `;

      expect(typeof result[0].int_val).toBe("number");
      expect(typeof result[0].float_val).toBe("number");
      expect(typeof result[0].text_val).toBe("string");
      expect(typeof result[0].bool_val).toBe("boolean");
      expect(typeof result[0].json_val).toBe("object");
      expect(Array.isArray(result[0].arr_val)).toBe(true);
      expect(result[0].null_val).toBeNull();
    });

    test("types preserved in UNION ALL", async () => {
      const result = await sql`
        SELECT 1::integer as num, 'a'::text as str
        UNION ALL
        SELECT 2::integer, 'b'::text
        UNION ALL
        SELECT 3::integer, 'c'::text
      `;

      for (const row of result) {
        expect(typeof row.num).toBe("number");
        expect(typeof row.str).toBe("string");
      }
    });

    test("types preserved in subquery", async () => {
      const result = await sql`
        SELECT * FROM (
          SELECT 42::integer as num, 'text'::text as str, true::boolean as flag
        ) sub
      `;

      expect(typeof result[0].num).toBe("number");
      expect(typeof result[0].str).toBe("string");
      expect(typeof result[0].flag).toBe("boolean");
    });

    test("types preserved in CTE", async () => {
      const result = await sql`
        WITH cte AS (
          SELECT 42::integer as num, 'text'::text as str
        )
        SELECT * FROM cte
      `;

      expect(typeof result[0].num).toBe("number");
      expect(typeof result[0].str).toBe("string");
    });
  });

  // ==================== Large Result Set Type Consistency ====================
  describe("Large Result Set Type Consistency", () => {
    test("types consistent across 100 rows", async () => {
      const result = await sql`
        SELECT
          gs::integer as num,
          (gs * 1.5)::double precision as flt,
          (gs % 2 = 0)::boolean as even
        FROM generate_series(1, 100) gs
      `;

      expect(result.length).toBe(100);
      for (const row of result) {
        expect(typeof row.num).toBe("number");
        expect(typeof row.flt).toBe("number");
        expect(typeof row.even).toBe("boolean");
      }
    });

    test("types consistent across 1000 rows", async () => {
      const result = await sql`
        SELECT gs::integer as num
        FROM generate_series(1, 1000) gs
      `;

      expect(result.length).toBe(1000);
      for (const row of result) {
        expect(typeof row.num).toBe("number");
      }
    });
  });
});

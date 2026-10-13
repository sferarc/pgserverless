/**
 * HTTP SQL Array Mode and Full Results Tests (30 tests)
 * Split from sql-query.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import type { Rows } from "../../helpers/types";

describe("HTTP SQL Modes Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("sql_modes");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        text_col TEXT
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // Array Mode Tests
  describe("Array Mode", () => {
    test("arrayMode false returns objects", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: false })`
        SELECT 1 as num, 'text' as str
      `;
      expect(result).toEqual([{ num: 1, str: "text" }]);
    });

    test("arrayMode true returns arrays", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT 1 as num, 'text' as str
      `;
      expect(result).toEqual([[1, "text"]]);
    });

    test("arrayMode with fullResults", async () => {
      const result = await neon(getDatabaseUrl(), {
        arrayMode: true,
        fullResults: true,
      })`SELECT 1 as num`;

      expect(result.rowAsArray).toBe(true);
      expect(result.rows).toEqual([[1]]);
    });

    test("type preservation in array mode", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT 42::integer, 'hello'::text, true::boolean
      `;
      expect(result[0]).toEqual([42, "hello", true]);
    });

    test("large result set in array mode", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT generate_series(1, 100) as n
      `;
      expect(result.length).toBe(100);
      expect(result[0]).toEqual([1]);
      expect(result[99]).toEqual([100]);
    });

    test("empty results in array mode", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT 1 WHERE false
      `;
      expect(result).toEqual([]);
    });

    test("single column results", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT 'only_one' as col
      `;
      expect(result).toEqual([["only_one"]]);
    });

    test("multiple column results", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT 1, 2, 3, 4, 5
      `;
      expect(result).toEqual([[1, 2, 3, 4, 5]]);
    });

    test("NULL handling in array mode", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT NULL::text, 'not null', NULL::integer
      `;
      expect(result).toEqual([[null, "not null", null]]);
    });

    test("binary data in array mode", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT '\\xDEADBEEF'::bytea
      `;
      expect(result.length).toBe(1);
      expect(result[0].length).toBe(1);
    });

    test("JSON in array mode", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT '{"key": "value"}'::jsonb
      `;
      expect(result).toEqual([[{ key: "value" }]]);
    });

    test("nested arrays preserved", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT ARRAY[1, 2, 3] as arr
      `;
      expect(result).toEqual([[[1, 2, 3]]]);
    });

    test("array mode via query() function", async () => {
      const sql = neon(getDatabaseUrl());
      const result = await sql.query("SELECT 1 as num", [], {
        arrayMode: true,
      });
      expect(result).toEqual([[1]]);
    });

    test("array mode override per query", async () => {
      const sql = neon(getDatabaseUrl(), { arrayMode: false });
      const objResult = await sql`SELECT 1 as num`;
      expect(objResult).toEqual([{ num: 1 }]);

      const arrResult = await sql.query("SELECT 1 as num", [], {
        arrayMode: true,
      });
      expect(arrResult).toEqual([[1]]);
    });

    test("array mode with many columns", async () => {
      const result = await neon(getDatabaseUrl(), { arrayMode: true })`
        SELECT 1 as a, 2 as b, 3 as c, 4 as d, 5 as e, 6 as f, 7 as g, 8 as h
      `;
      expect(result).toEqual([[1, 2, 3, 4, 5, 6, 7, 8]]);
    });
  });

  // Full Results Tests
  describe("Full Results", () => {
    test("fullResults false returns rows only", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: false })`
        SELECT 1 as num
      `;
      expect(result).toEqual([{ num: 1 }]);
      expect((result as Rows & { fields?: unknown }).fields).toBeUndefined();
    });

    test("fullResults true returns metadata", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        SELECT 1 as num
      `;
      expect(result.rows).toEqual([{ num: 1 }]);
      expect(result.command).toBe("SELECT");
      expect(result.rowCount).toBe(1);
      expect(result.fields).toBeDefined();
      expect(Array.isArray(result.fields)).toBe(true);
    });

    test("fields array structure", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        SELECT 1 as num, 'text' as str
      `;
      expect(result.fields.length).toBe(2);
      expect(result.fields[0].name).toBe("num");
      expect(result.fields[1].name).toBe("str");
      expect(result.fields[0].dataTypeID).toBeDefined();
    });

    test("command type SELECT", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        SELECT 1
      `;
      expect(result.command).toBe("SELECT");
    });

    test("command type INSERT", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        INSERT INTO ${sql.unsafe(testTable)} (text_col) VALUES ('test')
      `;
      expect(result.command).toBe("INSERT");
    });

    test("command type UPDATE", async () => {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (text_col) VALUES ('to_update')`;
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        UPDATE ${sql.unsafe(testTable)} SET text_col = 'updated' WHERE text_col = 'to_update'
      `;
      expect(result.command).toBe("UPDATE");
    });

    test("command type DELETE", async () => {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (text_col) VALUES ('to_delete')`;
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        DELETE FROM ${sql.unsafe(testTable)} WHERE text_col = 'to_delete'
      `;
      expect(result.command).toBe("DELETE");
    });

    test("rowCount accuracy for SELECT", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        SELECT generate_series(1, 5)
      `;
      expect(result.rowCount).toBe(5);
    });

    test("rowCount for INSERT", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        INSERT INTO ${sql.unsafe(testTable)} (text_col) VALUES ('a'), ('b'), ('c')
      `;
      expect(result.rowCount).toBe(3);
    });

    test("rowAsArray flag with arrayMode", async () => {
      const result = await neon(getDatabaseUrl(), {
        fullResults: true,
        arrayMode: true,
      })`SELECT 1`;
      expect(result.rowAsArray).toBe(true);
    });

    test("rowAsArray flag without arrayMode", async () => {
      const result = await neon(getDatabaseUrl(), {
        fullResults: true,
        arrayMode: false,
      })`SELECT 1`;
      expect(result.rowAsArray).toBe(false);
    });

    test("empty result metadata", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        SELECT 1 WHERE false
      `;
      expect(result.rows).toEqual([]);
      expect(result.rowCount).toBe(0);
      expect(result.command).toBe("SELECT");
    });

    test("query() with fullResults", async () => {
      const sql = neon(getDatabaseUrl());
      const result = await sql.query("SELECT 1 as num, 2 as num2", [], {
        fullResults: true,
      });
      expect(result.rows).toBeDefined();
      expect(result.fields).toBeDefined();
      expect(result.command).toBe("SELECT");
    });

    test("combined arrayMode and fullResults", async () => {
      const result = await neon(getDatabaseUrl(), {
        arrayMode: true,
        fullResults: true,
      })`SELECT 1 as num, 'text' as str`;

      expect(result.rows).toEqual([[1, "text"]]);
      expect(result.rowAsArray).toBe(true);
      expect(result.fields[0].name).toBe("num");
    });

    test("field dataTypeID present", async () => {
      const result = await neon(getDatabaseUrl(), { fullResults: true })`
        SELECT 1::integer as int_col, 'text'::text as text_col
      `;
      expect(result.fields[0].dataTypeID).toBeGreaterThan(0);
      expect(result.fields[1].dataTypeID).toBeGreaterThan(0);
    });
  });
});

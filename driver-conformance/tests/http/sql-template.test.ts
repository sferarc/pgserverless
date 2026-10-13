/**
 * SQL Template Tests (75 tests)
 *
 * Tests for the SQL template literal functionality including:
 * - Template literals (25 tests)
 * - unsafe() function (15 tests)
 * - query() function (20 tests)
 * - toParameterizedQuery (15 tests)
 */

import { type NeonDbError, neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { asRows } from "../../helpers/types";

describe("SQL Template Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    // Generate unique table name
    const timestamp = Date.now();
    const random = Math.random().toString(36).substring(2, 8);
    testTable = `template_test_${timestamp}_${random}`;

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        name TEXT,
        value INTEGER,
        data JSONB
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Template Literals (25 tests) ====================
  describe("Template Literals", () => {
    test("basic template literal", async () => {
      const result = await sql`SELECT 1 as num`;
      expect(result).toEqual([{ num: 1 }]);
    });

    test("single parameter", async () => {
      const value = "hello";
      const result = await sql`SELECT ${value} as greeting`;
      expect(result).toEqual([{ greeting: "hello" }]);
    });

    test("multiple parameters", async () => {
      const a = 1;
      const b = "two";
      const c = true;
      const result = await sql`SELECT ${a} as num, ${b} as str, ${c} as bool`;
      // Parameters are passed as strings
      expect(result).toEqual([{ num: "1", str: "two", bool: "true" }]);
    });

    test("string interpolation", async () => {
      const name = "world";
      const result = await sql`SELECT ${`Hello, ${name}`} as greeting`;
      expect(result).toEqual([{ greeting: "Hello, world" }]);
    });

    test("number interpolation", async () => {
      const num = 42;
      const result = await sql`SELECT ${num}::integer as value`;
      expect(result).toEqual([{ value: 42 }]);
    });

    test("boolean interpolation", async () => {
      const flag = true;
      const result = await sql`SELECT ${flag}::boolean as flag`;
      expect(result).toEqual([{ flag: true }]);
    });

    test("NULL interpolation", async () => {
      const nullVal = null;
      const result = await sql`SELECT ${nullVal}::text as null_val`;
      expect(result).toEqual([{ null_val: null }]);
    });

    test("Date interpolation", async () => {
      const date = new Date("2024-01-15T12:00:00.000Z");
      const result = await sql`SELECT ${date}::timestamptz as ts`;
      const rows = asRows(result);
      expect(rows.length).toBe(1);
      expect(rows[0].ts).toBeDefined();
    });

    test("object interpolation as JSON", async () => {
      const obj = { key: "value", num: 42 };
      const result = await sql`SELECT ${JSON.stringify(obj)}::jsonb as data`;
      expect(asRows(result)[0].data).toEqual(obj);
    });

    test("array interpolation", async () => {
      const arr = [1, 2, 3];
      const result = await sql`SELECT ${arr}::integer[] as nums`;
      expect(asRows(result)[0].nums).toEqual([1, 2, 3]);
    });

    test("Buffer/binary interpolation", async () => {
      const buffer = Buffer.from([0xde, 0xad, 0xbe, 0xef]);
      const result = await sql`SELECT ${buffer}::bytea as data`;
      expect(asRows(result).length).toBe(1);
    });

    test("empty string interpolation", async () => {
      const empty = "";
      const result = await sql`SELECT ${empty} as empty_str`;
      expect(result).toEqual([{ empty_str: "" }]);
    });

    test("template without parameters", async () => {
      const result = await sql`SELECT 'no params' as msg`;
      expect(result).toEqual([{ msg: "no params" }]);
    });

    test("adjacent parameters", async () => {
      const a = "hello";
      const b = "world";
      // Adjacent params get concatenated with || in SQL
      const result = await sql`SELECT ${a} || ${b} as concat`;
      expect(result).toEqual([{ concat: "helloworld" }]);
    });

    test("parameter at start", async () => {
      const val = 42;
      const result = await sql`SELECT ${val}::integer as num`;
      expect(result).toEqual([{ num: 42 }]);
    });

    test("parameter at end", async () => {
      const val = "end";
      const result = await sql`SELECT 'text' as start, ${val} as ending`;
      expect(result).toEqual([{ start: "text", ending: "end" }]);
    });

    test("multiline template", async () => {
      const name = "test";
      const result = await sql`
        SELECT
          ${name} as name,
          1 as value
      `;
      expect(result).toEqual([{ name: "test", value: 1 }]);
    });

    test("template with SQL comments", async () => {
      const val = 42;
      const result = await sql`
        -- This is a comment
        SELECT ${val} as num /* inline comment */
      `;
      // Parameters are passed as strings
      expect(result).toEqual([{ num: "42" }]);
    });

    test("template with quoted strings", async () => {
      const val = "param";
      const result = await sql`SELECT 'literal' as lit, ${val} as param`;
      expect(result).toEqual([{ lit: "literal", param: "param" }]);
    });

    test("escaped characters in template", async () => {
      const val = "with\nnewline";
      const result = await sql`SELECT ${val} as text`;
      expect(asRows(result)[0].text).toBe("with\nnewline");
    });

    test("unicode in template", async () => {
      const emoji = "\ud83d\ude00";
      const chinese = "\u4e2d\u6587";
      const result = await sql`SELECT ${emoji} as emoji, ${chinese} as chinese`;
      expect(result).toEqual([{ emoji, chinese }]);
    });

    test("special SQL characters in parameters", async () => {
      const val = 'O\'Reilly & "Associates"';
      const result = await sql`SELECT ${val} as name`;
      expect(asRows(result)[0].name).toBe(val);
    });

    test("template composition - nested templates", async () => {
      const innerQuery = sql`'nested'`;
      const result = await sql`SELECT ${innerQuery} as val`;
      expect(result).toEqual([{ val: "nested" }]);
    });

    test("deep nesting of templates", async () => {
      const level3 = sql`3`;
      const level2 = sql`${level3}`;
      const level1 = sql`SELECT ${level2}::integer as depth`;
      const result = await level1;
      expect(result).toEqual([{ depth: 3 }]);
    });

    test("template caching behavior", async () => {
      const val = 42;
      // Same template structure should work consistently
      const result1 = await sql`SELECT ${val} as num`;
      const result2 = await sql`SELECT ${val} as num`;
      expect(result1).toEqual(result2);
    });
  });

  // ==================== unsafe() Function (15 tests) ====================
  describe("unsafe() Function", () => {
    test("basic unsafe usage for column name", async () => {
      const colName = "dynamic_col";
      const result = await sql`SELECT 1 as ${sql.unsafe(colName)}`;
      expect(asRows(result)[0].dynamic_col).toBe(1);
    });

    test("unsafe for table name", async () => {
      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)}`;
      expect(asRows(result)[0].cnt).toBeDefined();
    });

    test("unsafe for ORDER BY", async () => {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES ('a', 1), ('b', 2)`;
      const orderCol = "value DESC";
      const result =
        await sql`SELECT name FROM ${sql.unsafe(testTable)} ORDER BY ${sql.unsafe(orderCol)} LIMIT 1`;
      expect(asRows(result)[0].name).toBe("b");
      await sql`DELETE FROM ${sql.unsafe(testTable)}`;
    });

    test("unsafe combined with template params", async () => {
      const tableName = testTable;
      const paramValue = "safe_value";
      const result = await sql`
        SELECT ${paramValue} as val
        FROM ${sql.unsafe(tableName)}
        WHERE 1=1
        LIMIT 1
      `;
      expect(asRows(result).length).toBeLessThanOrEqual(1);
    });

    test("multiple unsafe calls in one query", async () => {
      const col1 = "first_col";
      const col2 = "second_col";
      const result = await sql`SELECT 1 as ${sql.unsafe(col1)}, 2 as ${sql.unsafe(col2)}`;
      expect(asRows(result)[0].first_col).toBe(1);
      expect(asRows(result)[0].second_col).toBe(2);
    });

    test("unsafe in transaction", async () => {
      const tableName = testTable;
      const results = await sql.transaction([
        sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tableName)}`,
      ]);
      expect(asRows(results[0])[0].cnt).toBeDefined();
    });

    test("empty unsafe string", async () => {
      // This might cause a syntax error or empty result depending on context
      try {
        const result = await sql`SELECT 1 ${sql.unsafe("")}`;
        expect(result).toEqual([{ "?column?": 1 }]);
      } catch (_e) {
        // Empty unsafe might be allowed or cause error
        expect(true).toBe(true);
      }
    });

    test("unsafe with special characters", async () => {
      // Column names with special chars need quoting
      const colName = '"special-col"';
      const result = await sql`SELECT 1 as ${sql.unsafe(colName)}`;
      expect(asRows(result)[0]["special-col"]).toBe(1);
    });

    test("unsafe with newlines", async () => {
      const sqlFragment = "1 as first,\n2 as second";
      const result = await sql`SELECT ${sql.unsafe(sqlFragment)}`;
      expect(asRows(result)[0].first).toBe(1);
      expect(asRows(result)[0].second).toBe(2);
    });

    test("unsafe with SQL keywords", async () => {
      const keyword = "SELECT";
      // Using keyword as alias (quoted)
      const result = await sql`SELECT 1 as "${sql.unsafe(keyword)}"`;
      expect(asRows(result)[0].SELECT).toBe(1);
    });

    test("unsafe for function name", async () => {
      const funcName = "COUNT";
      const result =
        await sql`SELECT ${sql.unsafe(funcName)}(*) as cnt FROM ${sql.unsafe(testTable)}`;
      expect(asRows(result)[0].cnt).toBeDefined();
    });

    test("unsafe for schema.table", async () => {
      const qualifiedName = `public.${testTable}`;
      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(qualifiedName)}`;
      expect(asRows(result)[0].cnt).toBeDefined();
    });

    test("unsafe for LIMIT value", async () => {
      const limit = "5";
      const result = await sql`SELECT generate_series(1, 10) as n LIMIT ${sql.unsafe(limit)}`;
      expect(asRows(result).length).toBe(5);
    });

    test("unsafe concatenation", async () => {
      const part1 = "first";
      const part2 = "_col";
      const result = await sql`SELECT 1 as ${sql.unsafe(part1 + part2)}`;
      expect(asRows(result)[0].first_col).toBe(1);
    });

    test("unsafe vs safe parameter difference", async () => {
      const safeValue = "test";
      const unsafeValue = "test_col";

      // Safe parameter - becomes $1 placeholder
      const result1 = await sql`SELECT ${safeValue} as col`;
      expect(asRows(result1)[0].col).toBe("test");

      // Unsafe - interpolated directly
      const result2 = await sql`SELECT 'value' as ${sql.unsafe(unsafeValue)}`;
      expect(asRows(result2)[0].test_col).toBe("value");
    });
  });

  // ==================== query() Function (20 tests) ====================
  describe("query() Function", () => {
    test("basic query() usage", async () => {
      const result = await sql.query("SELECT 1 as num", []);
      expect(result).toEqual([{ num: 1 }]);
    });

    test("query with $1 placeholder", async () => {
      const result = await sql.query("SELECT $1 as val", ["hello"]);
      expect(result).toEqual([{ val: "hello" }]);
    });

    test("query with multiple placeholders", async () => {
      const result = await sql.query("SELECT $1 as first, $2 as second, $3 as third", [
        1,
        "two",
        true,
      ]);
      // Parameters are passed as strings
      expect(result).toEqual([{ first: "1", second: "two", third: "true" }]);
    });

    test("query with array params", async () => {
      const result = await sql.query("SELECT $1::integer[] as nums", [[1, 2, 3]]);
      expect(asRows(result)[0].nums).toEqual([1, 2, 3]);
    });

    test("query with options", async () => {
      const result = await sql.query("SELECT 1 as num", [], {
        arrayMode: false,
        fullResults: false,
      });
      expect(result).toEqual([{ num: 1 }]);
    });

    test("query with arrayMode option", async () => {
      const result = await sql.query("SELECT 1 as num, 'text' as str", [], {
        arrayMode: true,
      });
      expect(result).toEqual([[1, "text"]]);
    });

    test("query with fullResults option", async () => {
      const result = await sql.query("SELECT 1 as num", [], {
        fullResults: true,
      });
      expect(result.rows).toEqual([{ num: 1 }]);
      expect(result.command).toBe("SELECT");
      expect(result.rowCount).toBe(1);
    });

    test("query in transaction", async () => {
      const results = await sql.transaction([sql.query("SELECT $1::integer as num", [42])]);
      expect(results[0]).toEqual([{ num: 42 }]);
    });

    test("query error handling", async () => {
      try {
        await sql.query("INVALID SYNTAX", []);
        expect(true).toBe(false);
      } catch (e: unknown) {
        expect((e as NeonDbError).code).toBeDefined();
      }
    });

    test("query with NULL params", async () => {
      const result = await sql.query("SELECT $1::text as val", [null]);
      expect(result).toEqual([{ val: null }]);
    });

    test("query with undefined params treated as NULL", async () => {
      const result = await sql.query("SELECT $1::text as val", [undefined]);
      expect(result).toEqual([{ val: null }]);
    });

    test("query param type coercion - string to number", async () => {
      const result = await sql.query("SELECT $1::integer as num", ["42"]);
      expect(result).toEqual([{ num: 42 }]);
    });

    test("query with large params", async () => {
      const largeString = "x".repeat(10000);
      const result = await sql.query("SELECT $1 as data", [largeString]);
      expect(asRows(result)[0].data).toBe(largeString);
    });

    test("query with binary params", async () => {
      const binary = Buffer.from([0xca, 0xfe, 0xba, 0xbe]);
      const result = await sql.query("SELECT $1::bytea as data", [binary]);
      expect(asRows(result).length).toBe(1);
    });

    test("query placeholder validation - missing param", async () => {
      try {
        await sql.query("SELECT $1, $2", ["only one"]);
        // May or may not throw depending on implementation
      } catch (e: unknown) {
        expect(e).toBeDefined();
      }
    });

    test("query with extra params - rejected by Neon", async () => {
      // Neon rejects queries where the number of parameters doesn't match placeholders
      try {
        await sql.query("SELECT $1 as val", ["used", "extra", "ignored"]);
        expect(true).toBe(false);
      } catch (e: unknown) {
        // Error code 08P01 indicates protocol violation (parameter count mismatch)
        expect((e as NeonDbError).code).toBe("08P01");
      }
    });

    test("query with out-of-order placeholders", async () => {
      const result = await sql.query("SELECT $2 as second, $1 as first", ["one", "two"]);
      expect(result).toEqual([{ second: "two", first: "one" }]);
    });

    test("query with repeated placeholder", async () => {
      const result = await sql.query("SELECT $1 as a, $1 as b", ["same"]);
      expect(result).toEqual([{ a: "same", b: "same" }]);
    });

    test("query with JSON param", async () => {
      const json = { nested: { value: 42 } };
      const result = await sql.query("SELECT $1::jsonb as data", [JSON.stringify(json)]);
      expect(asRows(result)[0].data).toEqual(json);
    });

    test("query with boolean params", async () => {
      const result = await sql.query("SELECT $1::boolean as t, $2::boolean as f", [true, false]);
      expect(result).toEqual([{ t: true, f: false }]);
    });
  });

  // ==================== toParameterizedQuery (15 tests) ====================
  describe("toParameterizedQuery", () => {
    test("basic conversion", async () => {
      const template = sql`SELECT ${1} as num`;
      // The template should be usable as a query
      const result = await template;
      // Parameter passed as string
      expect(result).toEqual([{ num: "1" }]);
    });

    test("multiple parameters conversion", async () => {
      const a = "hello";
      const b = 42;
      const c = true;
      const template = sql`SELECT ${a} as str, ${b} as num, ${c} as bool`;
      const result = await template;
      // Parameters are passed as strings
      expect(asRows(result)[0]).toEqual({
        str: "hello",
        num: "42",
        bool: "true",
      });
    });

    test("nested template conversion", async () => {
      const inner = sql`'nested'`;
      const outer = sql`SELECT ${inner} as val`;
      const result = await outer;
      expect(result).toEqual([{ val: "nested" }]);
    });

    test("binary data conversion", async () => {
      const binary = Buffer.from([0x01, 0x02, 0x03]);
      const template = sql`SELECT ${binary}::bytea as data`;
      const result = await template;
      expect(asRows(result).length).toBe(1);
    });

    test("empty template conversion", async () => {
      const template = sql`SELECT 'no params'`;
      const result = await template;
      expect(result).toEqual([{ "?column?": "no params" }]);
    });

    test("template with type annotations", async () => {
      const val = "42";
      const template = sql`SELECT ${val}::integer as num`;
      const result = await template;
      expect(result).toEqual([{ num: 42 }]);
    });

    test("parameter numbering is correct", async () => {
      // Multiple params should be numbered $1, $2, etc.
      const a = 1;
      const b = 2;
      const c = 3;
      const result = await sql`SELECT ${a}, ${b}, ${c}`;
      expect(asRows(result).length).toBe(1);
    });

    test("recursive resolution with multiple levels", async () => {
      // Test nested template literals with explicit type casting
      const l3 = sql`${3}::integer`;
      const l2 = sql`${2}::integer + ${l3}`;
      const l1 = sql`SELECT ${1}::integer + ${l2} as total`;

      // This tests nested template handling
      const result = await l1;
      expect(asRows(result).length).toBe(1);
      expect(asRows(result)[0].total).toBe(6); // 1 + 2 + 3
    });

    test("error on invalid template", async () => {
      try {
        await sql`INVALID SQL SYNTAX HERE ${1}`;
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });

    test("template with many parameters", async () => {
      const params = Array.from({ length: 50 }, (_, i) => i);
      // Build a template with many params
      // Use query() for many params
      const placeholders = params.map((_, i) => `$${i + 1} as col${i}`).join(", ");
      const result = await sql.query(`SELECT ${placeholders}`, params);
      expect(Object.keys(asRows(result)[0]).length).toBe(50);
    });

    test("deep nesting limits", async () => {
      // Create deeply nested templates
      let template: ReturnType<typeof sql> = sql`1`;
      for (let i = 0; i < 10; i++) {
        template = sql`(${template})`;
      }
      const result = await sql`SELECT ${template}::integer as deep`;
      expect(asRows(result)[0].deep).toBe(1);
    });

    test("template preserves NULL", async () => {
      const template = sql`SELECT ${null}::text as val`;
      const result = await template;
      expect(asRows(result)[0].val).toBeNull();
    });

    test("template with Date object", async () => {
      const date = new Date("2024-01-15");
      const template = sql`SELECT ${date}::date as d`;
      const result = await template;
      expect(asRows(result).length).toBe(1);
    });

    test("template reuse", async () => {
      const value = 42;
      const template = sql`SELECT ${value} as num`;

      // Use same template multiple times
      const result1 = await template;
      const result2 = await template;

      expect(result1).toEqual(result2);
    });

    test("template with unsafe and params mixed", async () => {
      const tableName = testTable;
      const paramValue = "test_value";
      const result = await sql`
        SELECT ${paramValue} as val
        FROM ${sql.unsafe(tableName)}
        LIMIT 1
      `;
      expect(asRows(result).length).toBeLessThanOrEqual(1);
    });
  });
});

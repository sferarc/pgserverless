/**
 * HTTP SQL Data Types Tests (50 tests)
 * Split from sql-query.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { asRows } from "../../helpers/types";

describe("HTTP SQL Data Types Tests", () => {
  const sql = neon(getDatabaseUrl());

  test("smallint type", async () => {
    const result = await sql`SELECT 32767::smallint as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe(32767);
  });

  test("integer type", async () => {
    const result = await sql`SELECT 2147483647::integer as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe(2147483647);
  });

  test("bigint type", async () => {
    const result = await sql`SELECT 9223372036854775807::bigint as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("9223372036854775807");
  });

  test("real type", async () => {
    const result = await sql`SELECT 3.14::real as val`;
    const rows = asRows(result);
    expect(typeof rows[0].val).toBe("number");
    expect(rows[0].val).toBeCloseTo(3.14, 2);
  });

  test("double precision type", async () => {
    const result = await sql`SELECT 3.141592653589793::double precision as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeCloseTo(Math.PI, 10);
  });

  test("numeric type", async () => {
    const result = await sql`SELECT 123456.789012345::numeric(20,10) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("123456.7890123450");
  });

  test("decimal type", async () => {
    const result = await sql`SELECT 999.99::decimal(10,2) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("999.99");
  });

  test("char type", async () => {
    const result = await sql`SELECT 'abc'::char(5) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("abc  ");
  });

  test("varchar type", async () => {
    const result = await sql`SELECT 'hello'::varchar(100) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("hello");
  });

  test("text type", async () => {
    const result = await sql`SELECT 'unlimited text'::text as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("unlimited text");
  });

  test("bytea type", async () => {
    const result = await sql`SELECT '\\xDEADBEEF'::bytea as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("boolean true", async () => {
    const result = await sql`SELECT true::boolean as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe(true);
  });

  test("boolean false", async () => {
    const result = await sql`SELECT false::boolean as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe(false);
  });

  test("date type", async () => {
    const result = await sql`SELECT '2024-01-15'::date as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("time type", async () => {
    const result = await sql`SELECT '14:30:00'::time as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("14:30:00");
  });

  test("timestamp type", async () => {
    const result = await sql`SELECT '2024-01-15 14:30:00'::timestamp as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("timestamptz type", async () => {
    const result = await sql`SELECT '2024-01-15 14:30:00+00'::timestamptz as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("interval type", async () => {
    const result = await sql`SELECT '1 day 2 hours'::interval as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("uuid type", async () => {
    const uuid = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11";
    const result = await sql`SELECT ${uuid}::uuid as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe(uuid);
  });

  test("json type", async () => {
    const result = await sql`SELECT '{"key": "value"}'::json as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual({ key: "value" });
  });

  test("jsonb type", async () => {
    const result = await sql`SELECT '{"num": 42, "bool": true}'::jsonb as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual({ num: 42, bool: true });
  });

  test("integer array", async () => {
    const result = await sql`SELECT ARRAY[1, 2, 3]::integer[] as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual([1, 2, 3]);
  });

  test("text array", async () => {
    const result = await sql`SELECT ARRAY['a', 'b', 'c']::text[] as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual(["a", "b", "c"]);
  });

  test("boolean array", async () => {
    const result = await sql`SELECT ARRAY[true, false, true]::boolean[] as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual([true, false, true]);
  });

  test("nested array", async () => {
    const result = await sql`SELECT ARRAY[ARRAY[1,2], ARRAY[3,4]]::integer[][] as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual([
      [1, 2],
      [3, 4],
    ]);
  });

  test("empty array", async () => {
    const result = await sql`SELECT ARRAY[]::integer[] as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual([]);
  });

  test("point type", async () => {
    const result = await sql`SELECT '(1,2)'::point as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("inet type", async () => {
    const result = await sql`SELECT '192.168.1.1'::inet as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("192.168.1.1");
  });

  test("cidr type", async () => {
    const result = await sql`SELECT '192.168.1.0/24'::cidr as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("192.168.1.0/24");
  });

  test("macaddr type", async () => {
    const result = await sql`SELECT '08:00:2b:01:02:03'::macaddr as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("08:00:2b:01:02:03");
  });

  test("tsvector type", async () => {
    const result = await sql`SELECT 'the quick brown fox'::tsvector as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("tsquery type", async () => {
    const result = await sql`SELECT 'quick & fox'::tsquery as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeDefined();
  });

  test("int4range type", async () => {
    const result = await sql`SELECT '[1,10)'::int4range as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("[1,10)");
  });

  test("NULL values for each type", async () => {
    const result = await sql`
      SELECT
        NULL::integer as int_null,
        NULL::text as text_null,
        NULL::boolean as bool_null,
        NULL::jsonb as json_null
    `;
    const rows = asRows(result);
    expect(rows[0].int_null).toBeNull();
    expect(rows[0].text_null).toBeNull();
    expect(rows[0].bool_null).toBeNull();
    expect(rows[0].json_null).toBeNull();
  });

  test("infinity values", async () => {
    const result =
      await sql`SELECT 'infinity'::double precision as pos, '-infinity'::double precision as neg`;
    const rows = asRows(result);
    expect(rows[0].pos).toBe(Infinity);
    expect(rows[0].neg).toBe(-Infinity);
  });

  test("NaN value", async () => {
    const result = await sql`SELECT 'NaN'::double precision as val`;
    const rows = asRows(result);
    expect(Number.isNaN(rows[0].val)).toBe(true);
  });

  test("money type", async () => {
    const result = await sql`SELECT '$1,234.56'::money as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("$1,234.56");
  });

  test("bit type", async () => {
    const result = await sql`SELECT B'101010'::bit(6) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("101010");
  });

  test("bit varying type", async () => {
    const result = await sql`SELECT B'10101'::bit varying(10) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("10101");
  });

  test("complex JSON structure", async () => {
    const result = await sql`
      SELECT '{"arr": [1,2,3], "nested": {"a": "b"}, "null": null}'::jsonb as val
    `;
    const rows = asRows(result);
    expect(rows[0].val).toEqual({
      arr: [1, 2, 3],
      nested: { a: "b" },
      null: null,
    });
  });

  test("JSON array", async () => {
    const result = await sql`SELECT '[1, "two", true, null]'::jsonb as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual([1, "two", true, null]);
  });

  test("empty JSON object", async () => {
    const result = await sql`SELECT '{}'::jsonb as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual({});
  });

  test("empty JSON array", async () => {
    const result = await sql`SELECT '[]'::jsonb as val`;
    const rows = asRows(result);
    expect(rows[0].val).toEqual([]);
  });

  test("zero values", async () => {
    const result = await sql`
      SELECT 0::integer as int_zero, 0.0::double precision as float_zero, ''::text as empty_str
    `;
    const rows = asRows(result);
    expect(rows[0].int_zero).toBe(0);
    expect(rows[0].float_zero).toBe(0);
    expect(rows[0].empty_str).toBe("");
  });

  test("negative numbers", async () => {
    const result = await sql`SELECT -42::integer as neg_int, -3.14::double precision as neg_float`;
    const rows = asRows(result);
    expect(rows[0].neg_int).toBe(-42);
    expect(rows[0].neg_float).toBeCloseTo(-3.14, 2);
  });

  test("very large numeric", async () => {
    const result = await sql`SELECT '99999999999999999999.9999999999'::numeric as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("99999999999999999999.9999999999");
  });

  test("scientific notation", async () => {
    const result = await sql`SELECT 1.23e10::double precision as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe(1.23e10);
  });
});

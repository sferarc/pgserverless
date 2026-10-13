/**
 * NULL Handling Edge Case Tests
 *
 * Based on real bugs from PlanetScale:
 * - PlanetScale #72: SELECT NULL crashed the driver (atob(undefined))
 * - PlanetScale #73: NULL type metadata was undefined
 *
 * These tests verify NULL values are handled correctly in all positions
 * and combinations across both transports.
 */

import { Client, neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("NULL Handling Edge Cases", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  const sqlFull = neon(dbUrl, { fullResults: true });
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("null_test");
    await sql`CREATE TABLE ${sql.unsafe(testTable)} (
      id SERIAL PRIMARY KEY,
      text_col TEXT,
      int_col INTEGER,
      bool_col BOOLEAN,
      float_col DOUBLE PRECISION,
      json_col JSONB,
      bytea_col BYTEA,
      ts_col TIMESTAMPTZ,
      arr_col INTEGER[]
    )`;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== SELECT NULL (no table) ====================
  describe("Bare NULL Expressions", () => {
    test("SELECT NULL - single NULL column", async () => {
      const result = await sql`SELECT NULL as val`;
      expect(result[0].val).toBeNull();
    });

    test("SELECT NULL, 1 - NULL with non-NULL", async () => {
      const result = await sql`SELECT NULL as a, 1 as b`;
      expect(result[0].a).toBeNull();
      expect(result[0].b).toBe(1);
    });

    test("SELECT NULL, NULL - multiple NULLs", async () => {
      const result = await sql`SELECT NULL as a, NULL as b, NULL as c`;
      expect(result[0].a).toBeNull();
      expect(result[0].b).toBeNull();
      expect(result[0].c).toBeNull();
    });

    test("NULL with fullResults - field metadata", async () => {
      const result = await sqlFull`SELECT NULL as val`;
      expect(result.rows[0].val).toBeNull();
      expect(result.fields[0].name).toBe("val");
      expect(result.fields[0].dataTypeID).toBeDefined();
    });

    test("typed NULL values", async () => {
      const result = await sql`
        SELECT
          NULL::integer as int_null,
          NULL::text as text_null,
          NULL::boolean as bool_null,
          NULL::double precision as float_null,
          NULL::jsonb as json_null,
          NULL::bytea as bytea_null,
          NULL::timestamptz as ts_null,
          NULL::uuid as uuid_null
      `;
      expect(result[0].int_null).toBeNull();
      expect(result[0].text_null).toBeNull();
      expect(result[0].bool_null).toBeNull();
      expect(result[0].float_null).toBeNull();
      expect(result[0].json_null).toBeNull();
      expect(result[0].bytea_null).toBeNull();
      expect(result[0].ts_null).toBeNull();
      expect(result[0].uuid_null).toBeNull();
    });

    test("NULL in expressions", async () => {
      const result = await sql`
        SELECT
          NULL + 1 as add_null,
          NULL || 'text' as concat_null,
          COALESCE(NULL, 'default') as coalesce_val,
          NULLIF(1, 1) as nullif_val
      `;
      expect(result[0].add_null).toBeNull();
      expect(result[0].concat_null).toBeNull();
      expect(result[0].coalesce_val).toBe("default");
      expect(result[0].nullif_val).toBeNull();
    });
  });

  // ==================== NULL as Parameters ====================
  describe("NULL as Parameters", () => {
    test("NULL parameter in HTTP query", async () => {
      const result = await sql`SELECT ${null}::text as val`;
      expect(result[0].val).toBeNull();
    });

    test("NULL parameter in WHERE clause", async () => {
      const result = await sql`SELECT 1 as val WHERE ${null}::text IS NULL`;
      expect(result.length).toBe(1);
      expect(result[0].val).toBe(1);
    });

    test("NULL parameter via WebSocket client", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT $1::text as val", [null]);
      expect(result.rows[0].val).toBeNull();
      await client.end();
    });

    test("mixed NULL and non-NULL parameters", async () => {
      const result =
        await sql`SELECT ${null}::text as a, ${"hello"}::text as b, ${null}::integer as c, ${42}::integer as d`;
      expect(result[0].a).toBeNull();
      expect(result[0].b).toBe("hello");
      expect(result[0].c).toBeNull();
      expect(result[0].d).toBe(42);
    });
  });

  // ==================== NULL in Table Operations ====================
  describe("NULL in Table Operations", () => {
    test("INSERT and SELECT NULL values for all types", async () => {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col, bool_col, float_col, json_col, bytea_col, ts_col, arr_col)
        VALUES (NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)`;

      const result =
        await sql`SELECT text_col, int_col, bool_col, float_col, json_col, bytea_col, ts_col, arr_col FROM ${sql.unsafe(testTable)} ORDER BY id DESC LIMIT 1`;
      expect(result[0].text_col).toBeNull();
      expect(result[0].int_col).toBeNull();
      expect(result[0].bool_col).toBeNull();
      expect(result[0].float_col).toBeNull();
      expect(result[0].json_col).toBeNull();
      expect(result[0].bytea_col).toBeNull();
      expect(result[0].ts_col).toBeNull();
      expect(result[0].arr_col).toBeNull();
    });

    test("UPDATE to NULL", async () => {
      await sql`INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col) VALUES ('initial', 42)`;
      const inserted = await sql`SELECT id FROM ${sql.unsafe(testTable)} ORDER BY id DESC LIMIT 1`;
      const id = inserted[0].id;

      await sql`UPDATE ${sql.unsafe(testTable)} SET text_col = NULL, int_col = NULL WHERE id = ${id}`;
      const result =
        await sql`SELECT text_col, int_col FROM ${sql.unsafe(testTable)} WHERE id = ${id}`;
      expect(result[0].text_col).toBeNull();
      expect(result[0].int_col).toBeNull();
    });

    test("NULL in aggregate functions", async () => {
      const tbl = uniqueTableName("null_agg");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (val INTEGER)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1), (NULL), (3), (NULL), (5)`;

      const result = await sql`SELECT
        COUNT(*) as total,
        COUNT(val) as non_null,
        SUM(val) as sum_val,
        AVG(val) as avg_val
        FROM ${sql.unsafe(tbl)}`;

      expect(Number(result[0].total)).toBe(5);
      expect(Number(result[0].non_null)).toBe(3);
      expect(Number(result[0].sum_val)).toBe(9);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("NULL in CASE expressions", async () => {
      const result = await sql`SELECT CASE WHEN NULL THEN 'yes' ELSE 'no' END as val`;
      expect(result[0].val).toBe("no");
    });

    test("IS NULL and IS NOT NULL", async () => {
      const tbl = uniqueTableName("null_is");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (val TEXT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES ('a'), (NULL), ('b'), (NULL)`;

      const nulls = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)} WHERE val IS NULL`;
      const nonNulls =
        await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tbl)} WHERE val IS NOT NULL`;
      expect(Number(nulls[0].cnt)).toBe(2);
      expect(Number(nonNulls[0].cnt)).toBe(2);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== NULL in Arrays ====================
  describe("NULL in Arrays", () => {
    test("array with NULL elements", async () => {
      const result = await sql`SELECT ARRAY[1, NULL, 3]::integer[] as arr`;
      expect(result[0].arr).toEqual([1, null, 3]);
    });

    test("array of all NULLs", async () => {
      const result = await sql`SELECT ARRAY[NULL, NULL, NULL]::text[] as arr`;
      expect(result[0].arr).toEqual([null, null, null]);
    });

    test("NULL array vs empty array", async () => {
      const result = await sql`SELECT NULL::integer[] as null_arr, ARRAY[]::integer[] as empty_arr`;
      expect(result[0].null_arr).toBeNull();
      expect(result[0].empty_arr).toEqual([]);
    });
  });

  // ==================== NULL in JSON ====================
  describe("NULL in JSON", () => {
    test("JSON null vs SQL NULL", async () => {
      const result = await sql`SELECT 'null'::jsonb as json_null, NULL::jsonb as sql_null`;
      expect(result[0].json_null).toBeNull();
      expect(result[0].sql_null).toBeNull();
    });

    test("JSON object with null values", async () => {
      const result = await sql`SELECT '{"a": null, "b": 1}'::jsonb as val`;
      expect(result[0].val).toEqual({ a: null, b: 1 });
    });

    test("JSON array with null elements", async () => {
      const result = await sql`SELECT '[1, null, 3]'::jsonb as val`;
      expect(result[0].val).toEqual([1, null, 3]);
    });
  });

  // ==================== NULL via WebSocket ====================
  describe("NULL via WebSocket Client", () => {
    test("SELECT NULL via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT NULL as val");
      expect(result.rows[0].val).toBeNull();
      await client.end();
    });

    test("multiple NULL columns via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT NULL as a, NULL as b, 1 as c");
      expect(result.rows[0].a).toBeNull();
      expect(result.rows[0].b).toBeNull();
      expect(result.rows[0].c).toBe(1);
      await client.end();
    });

    test("NULL in parameterized INSERT/SELECT via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      await client.query(`INSERT INTO ${testTable} (text_col, int_col) VALUES ($1, $2)`, [
        null,
        null,
      ]);

      const result = await client.query(
        `SELECT text_col, int_col FROM ${testTable} ORDER BY id DESC LIMIT 1`,
      );
      expect(result.rows[0].text_col).toBeNull();
      expect(result.rows[0].int_col).toBeNull();

      await client.end();
    });
  });
});

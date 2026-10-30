/**
 * HTTP SQL Basic Query Tests (30 tests)
 * Split from sql-query.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("HTTP SQL Basic Query Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("sql_basic");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        text_col TEXT,
        int_col INTEGER,
        bool_col BOOLEAN
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  test("simple SELECT with literal", async () => {
    const result = await sql`SELECT 1 as num`;
    expect(result).toEqual([{ num: 1 }]);
  });

  test("SELECT with string parameter", async () => {
    const result = await sql`SELECT ${"hello world"} as str`;
    expect(result).toEqual([{ str: "hello world" }]);
  });

  test("SELECT with number parameter", async () => {
    const result = await sql`SELECT ${42}::integer as num`;
    expect(result).toEqual([{ num: 42 }]);
  });

  test("SELECT with boolean parameter - true", async () => {
    const result = await sql`SELECT ${true}::boolean as bool_val`;
    expect(result).toEqual([{ bool_val: true }]);
  });

  test("SELECT with boolean parameter - false", async () => {
    const result = await sql`SELECT ${false}::boolean as bool_val`;
    expect(result).toEqual([{ bool_val: false }]);
  });

  test("SELECT with NULL value", async () => {
    const result = await sql`SELECT ${null}::text as null_val`;
    expect(result).toEqual([{ null_val: null }]);
  });

  test("SELECT with empty string", async () => {
    const result = await sql`SELECT ${""} as empty_str`;
    expect(result).toEqual([{ empty_str: "" }]);
  });

  test("SELECT multiple columns", async () => {
    const result = await sql`SELECT ${"one"} as a, ${2}::integer as b, ${true}::boolean as c`;
    expect(result).toEqual([{ a: "one", b: 2, c: true }]);
  });

  test("SELECT with column aliases", async () => {
    const result = await sql`SELECT 1 as first_col, 'text' as second_col`;
    expect(result).toEqual([{ first_col: 1, second_col: "text" }]);
  });

  test("SELECT with expressions", async () => {
    const result = await sql`SELECT 1 + 2 as sum, 10 * 5 as product, 100 / 4 as quotient`;
    expect(result).toEqual([{ sum: 3, product: 50, quotient: 25 }]);
  });

  test("SELECT with type casts", async () => {
    const result = await sql`SELECT '42'::integer as num, 3.14::text as str`;
    expect(result).toEqual([{ num: 42, str: "3.14" }]);
  });

  test("SELECT with binary data (Uint8Array)", async () => {
    const data = new Uint8Array([0xde, 0xad, 0xbe, 0xef]);
    const result = await sql`SELECT ${data}::bytea as data`;
    const rows = asRows(result);
    expect(rows.length).toBe(1);
    expect(rows[0].data).toBeDefined();
  });

  test("SELECT with large text field", async () => {
    const largeText = "x".repeat(10000);
    const result = await sql`SELECT ${largeText} as large_text`;
    const rows = asRows(result);
    expect(rows[0].large_text).toBe(largeText);
  });

  test("INSERT single row", async () => {
    const result = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col)
      VALUES ('test', 1)
      RETURNING id
    `;
    const rows = asRows(result);
    expect(rows.length).toBe(1);
    expect(rows[0].id).toBeDefined();
  });

  test("INSERT multiple rows", async () => {
    const result = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col)
      VALUES ('a', 1), ('b', 2), ('c', 3)
      RETURNING id
    `;
    const rows = asRows(result);
    expect(rows.length).toBe(3);
  });

  test("INSERT with RETURNING", async () => {
    const result = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col, bool_col)
      VALUES ('returning test', 99, true)
      RETURNING id, text_col, int_col, bool_col
    `;
    const rows = asRows(result);
    expect(rows.length).toBe(1);
    expect(rows[0].text_col).toBe("returning test");
    expect(rows[0].int_col).toBe(99);
    expect(rows[0].bool_col).toBe(true);
  });

  test("UPDATE with parameters", async () => {
    const ins = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col)
      VALUES ('to update', 100)
      RETURNING id
    `;
    const insRows = asRows(ins);
    const id = insRows[0].id;

    await sql`
      UPDATE ${sql.unsafe(testTable)}
      SET text_col = 'updated', int_col = 200
      WHERE id = ${id}
    `;

    const result = await sql`
      SELECT text_col, int_col FROM ${sql.unsafe(testTable)} WHERE id = ${id}
    `;
    const rows = asRows(result);
    expect(rows[0].text_col).toBe("updated");
    expect(rows[0].int_col).toBe(200);
  });

  test("UPDATE with RETURNING", async () => {
    const ins = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col)
      VALUES ('before', 1)
      RETURNING id
    `;
    const insRows = asRows(ins);
    const id = insRows[0].id;

    const result = await sql`
      UPDATE ${sql.unsafe(testTable)}
      SET text_col = 'after', int_col = 2
      WHERE id = ${id}
      RETURNING text_col, int_col
    `;
    const rows = asRows(result);
    expect(rows[0].text_col).toBe("after");
    expect(rows[0].int_col).toBe(2);
  });

  test("DELETE with parameters", async () => {
    const ins = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col)
      VALUES ('to delete')
      RETURNING id
    `;
    const insRows = asRows(ins);
    const id = insRows[0].id;

    await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE id = ${id}`;

    const result = await sql`
      SELECT * FROM ${sql.unsafe(testTable)} WHERE id = ${id}
    `;
    const rows = asRows(result);
    expect(rows.length).toBe(0);
  });

  test("DELETE with RETURNING", async () => {
    const ins = await sql`
      INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col)
      VALUES ('delete me', 999)
      RETURNING id
    `;
    const insRows = asRows(ins);
    const id = insRows[0].id;

    const result = await sql`
      DELETE FROM ${sql.unsafe(testTable)}
      WHERE id = ${id}
      RETURNING text_col, int_col
    `;
    const rows = asRows(result);
    expect(rows[0].text_col).toBe("delete me");
    expect(rows[0].int_col).toBe(999);
  });

  test("CREATE TABLE", async () => {
    const tmpTable = uniqueTableName("create_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT PRIMARY KEY)`;
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("DROP TABLE", async () => {
    const tmpTable = uniqueTableName("drop_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`;
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("CREATE INDEX", async () => {
    const idxName = uniqueTableName("idx");
    await sql`CREATE INDEX ${sql.unsafe(idxName)} ON ${sql.unsafe(testTable)} (text_col)`;
    await sql`DROP INDEX ${sql.unsafe(idxName)}`;
  });

  test("ALTER TABLE add column", async () => {
    const tmpTable = uniqueTableName("alter_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`;
    await sql`ALTER TABLE ${sql.unsafe(tmpTable)} ADD COLUMN new_col TEXT`;
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("TRUNCATE table", async () => {
    const tmpTable = uniqueTableName("truncate_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`;
    await sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (1), (2), (3)`;
    await sql`TRUNCATE TABLE ${sql.unsafe(tmpTable)}`;
    const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(tmpTable)}`;
    const rows = asRows(result);
    expect(rows[0].cnt).toBe("0");
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("empty query handling", async () => {
    const result = await sql`SELECT 1 WHERE false`;
    expect(result).toEqual([]);
  });

  test("empty query string returns an empty result", async () => {
    const result = await sql.query("", [], { fullResults: true });
    expect(result.rows).toEqual([]);
    expect(result.fields).toEqual([]);
    expect(result.command).toBe("");
    expect(result.rowCount).toBeNull();
  });

  test("comment-only query returns result", async () => {
    const result = await sql`-- comment
SELECT 1 as val`;
    expect(result).toEqual([{ val: 1 }]);
  });

  test("Unicode in queries and params", async () => {
    const unicode = "こんにちは世界 🌍 مرحبا";
    const result = await sql`SELECT ${unicode} as unicode_str`;
    const rows = asRows(result);
    expect(rows[0].unicode_str).toBe(unicode);
  });

  test("special characters in params", async () => {
    const special = "'; DROP TABLE users; --";
    const result = await sql`SELECT ${special} as escaped`;
    const rows = asRows(result);
    expect(rows[0].escaped).toBe(special);
  });
});

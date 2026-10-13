/**
 * HTTP Basic Transaction Tests (25 tests)
 * Split from transactions.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("HTTP Basic Transaction Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("txn_basic");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL,
        value INTEGER DEFAULT 0,
        created_at TIMESTAMPTZ DEFAULT NOW()
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  test("single query transaction", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`]);
    expect(results.length).toBe(1);
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("multiple queries transaction", async () => {
    const results = await sql.transaction([
      sql`SELECT 1 as first`,
      sql`SELECT 2 as second`,
      sql`SELECT 3 as third`,
    ]);
    expect(results.length).toBe(3);
    expect(results[0]).toEqual([{ first: 1 }]);
    expect(results[1]).toEqual([{ second: 2 }]);
    expect(results[2]).toEqual([{ third: 3 }]);
  });

  test("transaction with parameters", async () => {
    const value = "test_value";
    const results = await sql.transaction([sql`SELECT ${value} as param`]);
    expect(results[0]).toEqual([{ param: "test_value" }]);
  });

  test("empty transaction", async () => {
    const results = await sql.transaction([]);
    expect(results).toEqual([]);
  });

  test("transaction returning values", async () => {
    const name = uniqueTableName("txn_ret");
    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 100) RETURNING id, name`,
    ]);
    expect(asRows(results[0]).length).toBe(1);
    expect(asRows(results[0])[0].name).toBe(name);
  });

  test("large transaction with many queries", async () => {
    const queries = Array.from({ length: 50 }, (_, i) => sql`SELECT ${i + 1} as num`);
    const results = await sql.transaction(queries);
    expect(results.length).toBe(50);
    expect(results[0]).toEqual([{ num: "1" }]);
    expect(results[49]).toEqual([{ num: "50" }]);
  });

  test("transaction with DDL", async () => {
    const tmpTable = uniqueTableName("txn_ddl");
    const results = await sql.transaction([
      sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`,
      sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (1)`,
      sql`SELECT * FROM ${sql.unsafe(tmpTable)}`,
    ]);
    expect(results.length).toBe(3);
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with DML mix", async () => {
    const name = uniqueTableName("txn_dml");
    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 1)`,
      sql`UPDATE ${sql.unsafe(testTable)} SET value = 2 WHERE name = ${name}`,
      sql`SELECT value FROM ${sql.unsafe(testTable)} WHERE name = ${name}`,
      sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name = ${name}`,
    ]);
    expect(results.length).toBe(4);
    expect(results[2]).toEqual([{ value: 2 }]);
  });

  test("transaction with errors causes rollback", async () => {
    const name = uniqueTableName("txn_rollback");
    try {
      await sql.transaction([
        sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 1)`,
        sql`SELECT 1/0`,
      ]);
      expect(true).toBe(false);
    } catch (_e) {
      // Expected error
    }
    const check = await sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE name = ${name}`;
    expect(asRows(check).length).toBe(0);
  });

  test("transaction isolation verification", async () => {
    const name1 = uniqueTableName("txn_iso1");
    const name2 = uniqueTableName("txn_iso2");

    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name1}, 10) RETURNING value`,
      sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name2}, 20) RETURNING value`,
      sql`SELECT SUM(value) as total FROM ${sql.unsafe(testTable)} WHERE name IN (${name1}, ${name2})`,
    ]);

    expect(asRows(results[2])[0].total).toBe("30");
    await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name IN (${name1}, ${name2})`;
  });

  test("transaction with temp table", async () => {
    const results = await sql.transaction([
      sql`CREATE TEMP TABLE temp_test (id INT)`,
      sql`INSERT INTO temp_test VALUES (1), (2), (3)`,
      sql`SELECT COUNT(*) as cnt FROM temp_test`,
    ]);
    expect(asRows(results[2])[0].cnt).toBe("3");
  });

  test("concurrent transactions", async () => {
    const name1 = uniqueTableName("concurrent1");
    const name2 = uniqueTableName("concurrent2");

    const [result1, result2] = await Promise.all([
      sql.transaction([
        sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name1}, 100) RETURNING name`,
      ]),
      sql.transaction([
        sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name2}, 200) RETURNING name`,
      ]),
    ]);

    expect(asRows(result1[0])[0].name).toBe(name1);
    expect(asRows(result2[0])[0].name).toBe(name2);

    await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name IN (${name1}, ${name2})`;
  });

  test("transaction with binary data", async () => {
    const tmpTable = uniqueTableName("txn_binary");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (data BYTEA)`;

    const binaryData = Buffer.from([0xde, 0xad, 0xbe, 0xef]);
    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(tmpTable)} (data) VALUES (${binaryData})`,
      sql`SELECT data FROM ${sql.unsafe(tmpTable)}`,
    ]);

    expect(results.length).toBe(2);
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with JSON data", async () => {
    const tmpTable = uniqueTableName("txn_json");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (data JSONB)`;

    const jsonData = { key: "value", nested: { arr: [1, 2, 3] } };
    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(tmpTable)} (data) VALUES (${JSON.stringify(jsonData)}::jsonb)`,
      sql`SELECT data FROM ${sql.unsafe(tmpTable)}`,
    ]);

    expect(asRows(results[1])[0].data).toEqual(jsonData);
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with array data", async () => {
    const tmpTable = uniqueTableName("txn_array");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (nums INTEGER[])`;

    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(tmpTable)} (nums) VALUES (ARRAY[1, 2, 3, 4, 5])`,
      sql`SELECT nums FROM ${sql.unsafe(tmpTable)}`,
    ]);

    expect(asRows(results[1])[0].nums).toEqual([1, 2, 3, 4, 5]);
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with NULL values", async () => {
    const tmpTable = uniqueTableName("txn_null");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (val TEXT)`;

    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(tmpTable)} (val) VALUES (NULL)`,
      sql`SELECT val FROM ${sql.unsafe(tmpTable)}`,
    ]);

    expect(asRows(results[1])[0].val).toBeNull();
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with unicode", async () => {
    const tmpTable = uniqueTableName("txn_unicode");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (val TEXT)`;

    const unicodeStr = "Hello \u4e16\u754c \ud83c\udf0d";
    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(tmpTable)} (val) VALUES (${unicodeStr})`,
      sql`SELECT val FROM ${sql.unsafe(tmpTable)}`,
    ]);

    expect(asRows(results[1])[0].val).toBe(unicodeStr);
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with special characters", async () => {
    const tmpTable = uniqueTableName("txn_special");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (val TEXT)`;

    const specialStr = 'O\'Reilly & "Associates"';
    const results = await sql.transaction([
      sql`INSERT INTO ${sql.unsafe(tmpTable)} (val) VALUES (${specialStr})`,
      sql`SELECT val FROM ${sql.unsafe(tmpTable)}`,
    ]);

    expect(asRows(results[1])[0].val).toBe(specialStr);
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("transaction with LOCK statement", async () => {
    const results = await sql.transaction([
      sql`LOCK TABLE ${sql.unsafe(testTable)} IN ACCESS SHARE MODE`,
      sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)}`,
    ]);
    expect(results.length).toBe(2);
  });

  test("transaction with advisory lock", async () => {
    const results = await sql.transaction([
      sql`SELECT pg_advisory_xact_lock(12345)`,
      sql`SELECT 1 as locked`,
    ]);
    expect(asRows(results[1])).toEqual([{ locked: 1 }]);
  });

  test("transaction with sequence", async () => {
    const seqName = uniqueTableName("txn_seq");
    await sql`CREATE SEQUENCE ${sql.unsafe(seqName)}`;

    const results = await sql.transaction([
      sql`SELECT nextval(${seqName}) as v1`,
      sql`SELECT nextval(${seqName}) as v2`,
      sql`SELECT nextval(${seqName}) as v3`,
    ]);

    expect(Number(asRows(results[0])[0].v1)).toBe(1);
    expect(Number(asRows(results[1])[0].v2)).toBe(2);
    expect(Number(asRows(results[2])[0].v3)).toBe(3);

    await sql`DROP SEQUENCE ${sql.unsafe(seqName)}`;
  });

  test("transaction with CTE", async () => {
    const results = await sql.transaction([
      sql`
        WITH inserted AS (
          INSERT INTO ${sql.unsafe(testTable)} (name, value)
          VALUES (${uniqueTableName("cte")}, 999)
          RETURNING id, name, value
        )
        SELECT * FROM inserted
      `,
    ]);
    expect(asRows(results[0]).length).toBe(1);
    expect(asRows(results[0])[0].value).toBe(999);
  });
});

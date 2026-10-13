/**
 * HTTP Transaction Isolation Level Tests (25 tests)
 * Split from transactions.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("HTTP Transaction Isolation Level Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("txn_isolation");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL,
        value INTEGER DEFAULT 0
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  test("ReadUncommitted level (defaults to ReadCommitted)", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      isolationLevel: "ReadUncommitted",
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("ReadCommitted level (default)", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      isolationLevel: "ReadCommitted",
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("RepeatableRead level", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      isolationLevel: "RepeatableRead",
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("Serializable level", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      isolationLevel: "Serializable",
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("isolation level with INSERT", async () => {
    const name = uniqueTableName("iso_insert");
    const results = await sql.transaction(
      [sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 42) RETURNING *`],
      { isolationLevel: "Serializable" },
    );
    expect(asRows(results[0])[0].value).toBe(42);
    await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name = ${name}`;
  });

  test("isolation level with UPDATE", async () => {
    const name = uniqueTableName("iso_update");
    await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 10)`;

    const results = await sql.transaction(
      [sql`UPDATE ${sql.unsafe(testTable)} SET value = 20 WHERE name = ${name} RETURNING value`],
      { isolationLevel: "RepeatableRead" },
    );
    expect(asRows(results[0])[0].value).toBe(20);
    await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name = ${name}`;
  });

  test("isolation level with DELETE", async () => {
    const name = uniqueTableName("iso_delete");
    await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 30)`;

    const results = await sql.transaction(
      [sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name = ${name} RETURNING *`],
      { isolationLevel: "Serializable" },
    );
    expect(asRows(results[0]).length).toBe(1);
  });

  test("default isolation behavior", async () => {
    const results = await sql.transaction([
      sql`SELECT current_setting('transaction_isolation') as level`,
    ]);
    expect(asRows(results[0]).length).toBe(1);
  });

  test("isolation level with multiple queries", async () => {
    const results = await sql.transaction(
      [sql`SELECT 1 as first`, sql`SELECT 2 as second`, sql`SELECT 3 as third`],
      { isolationLevel: "Serializable" },
    );
    expect(results.length).toBe(3);
  });

  test("isolation level with DDL", async () => {
    const tmpTable = uniqueTableName("iso_ddl");
    const results = await sql.transaction(
      [
        sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`,
        sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (1)`,
        sql`SELECT * FROM ${sql.unsafe(tmpTable)}`,
      ],
      { isolationLevel: "ReadCommitted" },
    );
    expect(results[2]).toEqual([{ id: 1 }]);
    await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
  });

  test("isolation level with sequence", async () => {
    const seqName = uniqueTableName("iso_seq");
    await sql`CREATE SEQUENCE ${sql.unsafe(seqName)}`;

    const results = await sql.transaction([sql`SELECT nextval(${seqName}) as val`], {
      isolationLevel: "Serializable",
    });
    expect(Number(asRows(results[0])[0].val)).toBe(1);

    await sql`DROP SEQUENCE ${sql.unsafe(seqName)}`;
  });

  test("isolation level with CTE", async () => {
    const results = await sql.transaction(
      [
        sql`
          WITH nums AS (SELECT generate_series(1, 5) as n)
          SELECT SUM(n) as total FROM nums
        `,
      ],
      { isolationLevel: "RepeatableRead" },
    );
    expect(Number(asRows(results[0])[0].total)).toBe(15);
  });

  test("isolation level with subquery", async () => {
    const results = await sql.transaction(
      [sql`SELECT * FROM (SELECT 1 as num UNION SELECT 2) t ORDER BY num`],
      { isolationLevel: "Serializable" },
    );
    expect(asRows(results[0]).length).toBe(2);
  });

  test("isolation level with aggregate", async () => {
    const results = await sql.transaction(
      [sql`SELECT COUNT(*), MAX(value), MIN(value) FROM ${sql.unsafe(testTable)}`],
      { isolationLevel: "ReadCommitted" },
    );
    expect(asRows(results[0]).length).toBe(1);
  });

  test("isolation level with window function", async () => {
    const results = await sql.transaction(
      [sql`SELECT row_number() OVER (ORDER BY id) as rn FROM ${sql.unsafe(testTable)} LIMIT 5`],
      { isolationLevel: "RepeatableRead" },
    );
    expect(results.length).toBe(1);
  });

  test("isolation level with CASE expression", async () => {
    const results = await sql.transaction(
      [sql`SELECT CASE WHEN 1=1 THEN 'yes' ELSE 'no' END as result`],
      { isolationLevel: "Serializable" },
    );
    expect(asRows(results[0])[0].result).toBe("yes");
  });

  test("isolation level with COALESCE", async () => {
    const results = await sql.transaction([sql`SELECT COALESCE(NULL, NULL, 'default') as val`], {
      isolationLevel: "ReadCommitted",
    });
    expect(asRows(results[0])[0].val).toBe("default");
  });

  test("isolation level with NULLIF", async () => {
    const results = await sql.transaction(
      [sql`SELECT NULLIF(1, 1) as null_result, NULLIF(1, 2) as one`],
      { isolationLevel: "RepeatableRead" },
    );
    expect(asRows(results[0])[0].null_result).toBeNull();
    expect(asRows(results[0])[0].one).toBe(1);
  });

  test("isolation level with string functions", async () => {
    const results = await sql.transaction(
      [sql`SELECT UPPER('hello') as up, LOWER('WORLD') as low`],
      { isolationLevel: "Serializable" },
    );
    expect(asRows(results[0])[0].up).toBe("HELLO");
    expect(asRows(results[0])[0].low).toBe("world");
  });

  test("isolation level with date functions", async () => {
    const results = await sql.transaction(
      [sql`SELECT CURRENT_DATE as today, CURRENT_TIME as now`],
      { isolationLevel: "ReadCommitted" },
    );
    expect(asRows(results[0])[0].today).toBeDefined();
  });

  test("isolation level with math functions", async () => {
    const results = await sql.transaction([sql`SELECT ABS(-5) as abs_val, ROUND(3.7) as rounded`], {
      isolationLevel: "RepeatableRead",
    });
    expect(asRows(results[0])[0].abs_val).toBe(5);
  });

  test("isolation level with array functions", async () => {
    const results = await sql.transaction([sql`SELECT array_length(ARRAY[1,2,3], 1) as len`], {
      isolationLevel: "Serializable",
    });
    expect(asRows(results[0])[0].len).toBe(3);
  });

  test("isolation level with JSON functions", async () => {
    const results = await sql.transaction([sql`SELECT '{"a": 1}'::jsonb -> 'a' as val`], {
      isolationLevel: "ReadCommitted",
    });
    expect(asRows(results[0])[0].val).toBe(1);
  });
});

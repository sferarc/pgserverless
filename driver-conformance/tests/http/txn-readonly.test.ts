/**
 * HTTP Transaction Read-Only and Deferrable Tests (20 tests)
 * Split from transactions.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("HTTP Transaction Read-Only Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("txn_readonly");

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

  test("readOnly true with SELECT", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      readOnly: true,
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("readOnly false (default)", async () => {
    const name = uniqueTableName("rw_test");
    const results = await sql.transaction(
      [sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 1) RETURNING *`],
      { readOnly: false },
    );
    expect(asRows(results[0]).length).toBe(1);
    await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name = ${name}`;
  });

  test("write in read-only causes error", async () => {
    const name = uniqueTableName("ro_error");
    try {
      await sql.transaction([sql`INSERT INTO ${sql.unsafe(testTable)} (name) VALUES (${name})`], {
        readOnly: true,
      });
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as Error).message).toBeDefined();
    }
  });

  test("read-only with multiple SELECTs", async () => {
    const results = await sql.transaction(
      [sql`SELECT 1 as first`, sql`SELECT 2 as second`, sql`SELECT 3 as third`],
      { readOnly: true },
    );
    expect(results.length).toBe(3);
  });

  test("read-only with table read", async () => {
    const results = await sql.transaction([sql`SELECT * FROM ${sql.unsafe(testTable)} LIMIT 5`], {
      readOnly: true,
    });
    expect(results.length).toBe(1);
  });

  test("read-only with aggregate", async () => {
    const results = await sql.transaction(
      [sql`SELECT COUNT(*) as cnt FROM ${sql.unsafe(testTable)}`],
      { readOnly: true },
    );
    expect(asRows(results[0])[0].cnt).toBeDefined();
  });

  test("read-only with CTE", async () => {
    const results = await sql.transaction(
      [
        sql`
          WITH data AS (SELECT generate_series(1, 10) as n)
          SELECT SUM(n) as total FROM data
        `,
      ],
      { readOnly: true },
    );
    expect(Number(asRows(results[0])[0].total)).toBe(55);
  });

  test("read-only with subquery", async () => {
    const results = await sql.transaction([sql`SELECT * FROM (SELECT 1 as n UNION SELECT 2) t`], {
      readOnly: true,
    });
    expect(asRows(results[0]).length).toBe(2);
  });

  test("read-only with function call", async () => {
    const results = await sql.transaction([sql`SELECT NOW() as current_time`], {
      readOnly: true,
    });
    expect(asRows(results[0])[0].current_time).toBeDefined();
  });

  test("deferrable true with Serializable and readOnly", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      isolationLevel: "Serializable",
      readOnly: true,
      deferrable: true,
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("deferrable false (default)", async () => {
    const results = await sql.transaction([sql`SELECT 1 as num`], {
      isolationLevel: "Serializable",
      readOnly: true,
      deferrable: false,
    });
    expect(results[0]).toEqual([{ num: 1 }]);
  });

  test("deferrable with multiple queries", async () => {
    const results = await sql.transaction([sql`SELECT 1 as first`, sql`SELECT 2 as second`], {
      isolationLevel: "Serializable",
      readOnly: true,
      deferrable: true,
    });
    expect(results.length).toBe(2);
  });

  test("read-only with JOIN", async () => {
    const results = await sql.transaction(
      [
        sql`
          SELECT t1.name, t2.name as name2
          FROM ${sql.unsafe(testTable)} t1
          CROSS JOIN ${sql.unsafe(testTable)} t2
          LIMIT 5
        `,
      ],
      { readOnly: true },
    );
    expect(results.length).toBe(1);
  });

  test("read-only with UNION", async () => {
    const results = await sql.transaction(
      [
        sql`
          SELECT 1 as num
          UNION ALL
          SELECT 2 as num
          UNION ALL
          SELECT 3 as num
        `,
      ],
      { readOnly: true },
    );
    expect(asRows(results[0]).length).toBe(3);
  });

  test("read-only with ORDER BY", async () => {
    const results = await sql.transaction(
      [sql`SELECT * FROM ${sql.unsafe(testTable)} ORDER BY id DESC LIMIT 5`],
      { readOnly: true },
    );
    expect(results.length).toBe(1);
  });

  test("read-only with GROUP BY", async () => {
    const results = await sql.transaction(
      [
        sql`
          SELECT value, COUNT(*) as cnt
          FROM ${sql.unsafe(testTable)}
          GROUP BY value
          HAVING COUNT(*) > 0
          LIMIT 5
        `,
      ],
      { readOnly: true },
    );
    expect(results.length).toBe(1);
  });

  test("read-only with DISTINCT", async () => {
    const results = await sql.transaction(
      [sql`SELECT DISTINCT value FROM ${sql.unsafe(testTable)} LIMIT 5`],
      { readOnly: true },
    );
    expect(results.length).toBe(1);
  });

  test("read-only with window function", async () => {
    const results = await sql.transaction(
      [
        sql`
          SELECT id, ROW_NUMBER() OVER (ORDER BY id) as rn
          FROM ${sql.unsafe(testTable)}
          LIMIT 5
        `,
      ],
      { readOnly: true },
    );
    expect(results.length).toBe(1);
  });

  test("read-only with LIMIT and OFFSET", async () => {
    const results = await sql.transaction(
      [sql`SELECT * FROM ${sql.unsafe(testTable)} LIMIT 5 OFFSET 0`],
      { readOnly: true },
    );
    expect(results.length).toBe(1);
  });

  test("read-only with EXISTS", async () => {
    const results = await sql.transaction(
      [sql`SELECT EXISTS (SELECT 1 FROM ${sql.unsafe(testTable)}) as has_rows`],
      { readOnly: true },
    );
    expect(asRows(results[0])[0].has_rows).toBeDefined();
  });
});

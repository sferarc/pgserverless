/**
 * HTTP Transaction Callbacks and Batch Tests (30 tests)
 * Split from transactions.test.ts for parallel execution
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("HTTP Transaction Callbacks and Batch Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("txn_callbacks");

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

  // Transaction Callbacks Tests
  describe("Transaction Callbacks", () => {
    test("array-style transaction", async () => {
      const results = await sql.transaction([sql`SELECT 1 as num`, sql`SELECT 2 as num`]);
      expect(results.length).toBe(2);
    });

    test("function-style transaction", async () => {
      const results = await sql.transaction((txn) => [txn`SELECT 1 as num`, txn`SELECT 2 as num`]);
      expect(results.length).toBe(2);
    });

    test("function receives sql parameter", async () => {
      const results = await sql.transaction((txn) => {
        return [txn`SELECT 'from callback' as source`];
      });
      expect(asRows(results[0])[0].source).toBe("from callback");
    });

    test("query composition in function", async () => {
      const results = await sql.transaction((txn) => {
        const value = 42;
        return [txn`SELECT ${value} as val`, txn`SELECT ${value * 2} as doubled`];
      });
      expect(asRows(results[0])[0].val).toBe("42");
      expect(asRows(results[1])[0].doubled).toBe("84");
    });

    test("error in function callback", async () => {
      try {
        await sql.transaction(() => {
          throw new Error("Intentional error");
        });
        expect(true).toBe(false);
      } catch (e: unknown) {
        expect((e as Error).message).toBe("Intentional error");
      }
    });

    test("return value validation - array of queries", async () => {
      const results = await sql.transaction((txn) => [
        txn`SELECT 1 as first`,
        txn`SELECT 2 as second`,
      ]);
      expect(Array.isArray(results)).toBe(true);
    });

    test("transaction with unsafe() in callback", async () => {
      const colName = "dynamic_col";
      const results = await sql.transaction((txn) => [txn`SELECT 1 as ${txn.unsafe(colName)}`]);
      expect(asRows(results[0])[0].dynamic_col).toBe(1);
    });

    test("transaction with query() in callback", async () => {
      const results = await sql.transaction((txn) => [
        txn.query("SELECT $1::integer as num", [42]),
      ]);
      expect(asRows(results[0])[0].num).toBe(42);
    });

    test("mixed template and query() in callback", async () => {
      const results = await sql.transaction((txn) => [
        txn`SELECT 'template' as source`,
        txn.query("SELECT 'query' as source", []),
      ]);
      expect(asRows(results[0])[0].source).toBe("template");
      expect(asRows(results[1])[0].source).toBe("query");
    });

    test("callback with options override", async () => {
      const results = await sql.transaction((txn) => [txn`SELECT 1 as num`], {
        isolationLevel: "Serializable",
      });
      expect(results.length).toBe(1);
    });

    test("callback with multiple parameters", async () => {
      const results = await sql.transaction((txn) => {
        const a = 1;
        const b = "text";
        const c = true;
        return [txn`SELECT ${a} as num, ${b} as str, ${c} as bool`];
      });
      expect(asRows(results[0])[0]).toEqual({
        num: "1",
        str: "text",
        bool: "true",
      });
    });

    test("callback building queries dynamically", async () => {
      const count = 5;
      const results = await sql.transaction((txn) =>
        Array.from({ length: count }, (_, i) => txn`SELECT ${i + 1} as num`),
      );
      expect(results.length).toBe(5);
      expect(asRows(results[4])[0].num).toBe("5");
    });

    test("callback with conditional queries", async () => {
      const includeExtra = true;
      const results = await sql.transaction((txn) => {
        const queries = [txn`SELECT 1 as base`];
        if (includeExtra) {
          queries.push(txn`SELECT 2 as extra`);
        }
        return queries;
      });
      expect(results.length).toBe(2);
    });

    test("callback with fullResults option", async () => {
      const results = await sql.transaction((txn) => [txn`SELECT 1 as num`], {
        fullResults: true,
      });
      expect(results[0].rows).toEqual([{ num: 1 }]);
      expect(results[0].command).toBe("SELECT");
    });

    test("callback with arrayMode option", async () => {
      const results = await sql.transaction((txn) => [txn`SELECT 1 as num, 'text' as str`], {
        arrayMode: true,
      });
      expect(results[0]).toEqual([[1, "text"]]);
    });
  });

  // Batch Request Format Tests
  describe("Batch Request Format", () => {
    test("batch with single query", async () => {
      const results = await sql.transaction([sql`SELECT 1 as num`]);
      expect(results.length).toBe(1);
    });

    test("batch with multiple queries", async () => {
      const results = await sql.transaction([
        sql`SELECT 1 as a`,
        sql`SELECT 2 as b`,
        sql`SELECT 3 as c`,
      ]);
      expect(results.length).toBe(3);
    });

    test("batch error handling - all fail on error", async () => {
      const name = uniqueTableName("batch_err");
      try {
        await sql.transaction([
          sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name}, 1)`,
          sql`SELECT 1/0`,
          sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${`${name}_2`}, 2)`,
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      const check = await sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE name LIKE ${`${name}%`}`;
      expect(asRows(check).length).toBe(0);
    });

    test("batch ordering guarantee", async () => {
      const results = await sql.transaction([
        sql`SELECT 1 as num`,
        sql`SELECT 2 as num`,
        sql`SELECT 3 as num`,
        sql`SELECT 4 as num`,
        sql`SELECT 5 as num`,
      ]);
      expect(asRows(results[0])[0].num).toBe(1);
      expect(asRows(results[1])[0].num).toBe(2);
      expect(asRows(results[2])[0].num).toBe(3);
      expect(asRows(results[3])[0].num).toBe(4);
      expect(asRows(results[4])[0].num).toBe(5);
    });

    test("batch with large queries", async () => {
      const longString = "x".repeat(10000);
      const results = await sql.transaction([sql`SELECT ${longString} as long_val`]);
      expect(asRows(results[0])[0].long_val).toBe(longString);
    });

    test("batch with different param types", async () => {
      const results = await sql.transaction([
        sql`SELECT ${42}::integer as int_val`,
        sql`SELECT ${"text"}::text as text_val`,
        sql`SELECT ${true}::boolean as bool_val`,
        sql`SELECT ${null}::text as null_val`,
      ]);
      expect(asRows(results[0])[0].int_val).toBe(42);
      expect(asRows(results[1])[0].text_val).toBe("text");
      expect(asRows(results[2])[0].bool_val).toBe(true);
      expect(asRows(results[3])[0].null_val).toBeNull();
    });

    test("batch with binary params", async () => {
      const tmpTable = uniqueTableName("batch_binary");
      await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (data BYTEA)`;

      const binaryData = Buffer.from([0x01, 0x02, 0x03]);
      const results = await sql.transaction([
        sql`INSERT INTO ${sql.unsafe(tmpTable)} (data) VALUES (${binaryData})`,
        sql`SELECT data FROM ${sql.unsafe(tmpTable)}`,
      ]);

      expect(results.length).toBe(2);
      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    });

    test("batch response structure with fullResults", async () => {
      const results = await sql.transaction([sql`SELECT 1 as num`, sql`SELECT 2 as num`], {
        fullResults: true,
      });

      expect(results[0].rows).toBeDefined();
      expect(results[0].fields).toBeDefined();
      expect(results[0].command).toBe("SELECT");
      expect(results[1].rows).toBeDefined();
    });

    test("batch with 100 queries", async () => {
      const queries = Array.from({ length: 100 }, (_, i) => sql`SELECT ${i} as idx`);
      const results = await sql.transaction(queries);
      expect(results.length).toBe(100);
    });

    test("batch with INSERT and RETURNING", async () => {
      const name1 = uniqueTableName("batch1");
      const name2 = uniqueTableName("batch2");

      const results = await sql.transaction([
        sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name1}, 10) RETURNING id`,
        sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES (${name2}, 20) RETURNING id`,
      ]);

      expect(asRows(results[0])[0].id).toBeDefined();
      expect(asRows(results[1])[0].id).toBeDefined();

      await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name IN (${name1}, ${name2})`;
    });

    test("batch with mixed commands", async () => {
      const tmpTable = uniqueTableName("batch_mix");

      const results = await sql.transaction([
        sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`,
        sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (1), (2), (3)`,
        sql`UPDATE ${sql.unsafe(tmpTable)} SET id = id * 10`,
        sql`SELECT * FROM ${sql.unsafe(tmpTable)} ORDER BY id`,
        sql`DELETE FROM ${sql.unsafe(tmpTable)} WHERE id < 30`,
        sql`SELECT COUNT(*) as remaining FROM ${sql.unsafe(tmpTable)}`,
      ]);

      expect(results.length).toBe(6);
      expect(Number(asRows(results[5])[0].remaining)).toBe(1);

      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    });

    test("batch idempotency - same queries produce same results", async () => {
      const queries = [sql`SELECT 1 as num`, sql`SELECT 'text' as str`];

      const results1 = await sql.transaction(queries);
      const results2 = await sql.transaction(queries);

      expect(results1).toEqual(results2);
    });

    test("batch with NULL results", async () => {
      const results = await sql.transaction([
        sql`SELECT NULL::integer as null_int`,
        sql`SELECT NULL::text as null_text`,
      ]);
      expect(asRows(results[0])[0].null_int).toBeNull();
      expect(asRows(results[1])[0].null_text).toBeNull();
    });

    test("batch with empty results", async () => {
      const results = await sql.transaction([sql`SELECT 1 WHERE false`, sql`SELECT 2 WHERE false`]);
      expect(results[0]).toEqual([]);
      expect(results[1]).toEqual([]);
    });
  });
});

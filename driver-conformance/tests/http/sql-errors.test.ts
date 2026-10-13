/**
 * HTTP SQL Error Handling Tests (40 tests)
 * Split from sql-query.test.ts for parallel execution
 */

import { type NeonDbError, neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

const pgErrorCodes = {
  syntaxError: "42601",
  undefinedTable: "42P01",
  undefinedColumn: "42703",
  divisionByZero: "22012",
  uniqueViolation: "23505",
  foreignKeyViolation: "23503",
  notNullViolation: "23502",
  checkViolation: "23514",
  invalidTextRepresentation: "22P02",
  numericValueOutOfRange: "22003",
  duplicateTable: "42P07",
};

describe("HTTP SQL Error Handling Tests", () => {
  let testTable: string;
  let sql: ReturnType<typeof neon>;

  beforeAll(async () => {
    sql = neon(getDatabaseUrl());
    testTable = uniqueTableName("sql_errors");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        text_col TEXT,
        int_col INTEGER
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  test("syntax error", async () => {
    try {
      await sql`SELECTT 1`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.syntaxError);
    }
  });

  test("undefined table", async () => {
    try {
      await sql`SELECT * FROM nonexistent_table_12345`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.undefinedTable);
    }
  });

  test("undefined column", async () => {
    try {
      await sql`SELECT nonexistent_column FROM ${sql.unsafe(testTable)}`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.undefinedColumn);
    }
  });

  test("division by zero", async () => {
    try {
      await sql`SELECT 1/0`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.divisionByZero);
    }
  });

  test("unique violation", async () => {
    const tmpTable = uniqueTableName("unique_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT PRIMARY KEY)`;

    try {
      await sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (1)`;
      await sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (1)`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.uniqueViolation);
    } finally {
      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    }
  });

  test("not null violation", async () => {
    const tmpTable = uniqueTableName("notnull_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT NOT NULL)`;

    try {
      await sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (NULL)`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.notNullViolation);
    } finally {
      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    }
  });

  test("check constraint violation", async () => {
    const tmpTable = uniqueTableName("check_test");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (val INT CHECK (val > 0))`;

    try {
      await sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (-1)`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.checkViolation);
    } finally {
      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    }
  });

  test("foreign key violation", async () => {
    const parentTable = uniqueTableName("fk_parent");
    const childTable = uniqueTableName("fk_child");

    await sql`CREATE TABLE ${sql.unsafe(parentTable)} (id INT PRIMARY KEY)`;
    await sql`CREATE TABLE ${sql.unsafe(childTable)} (
      id INT,
      parent_id INT REFERENCES ${sql.unsafe(parentTable)}(id)
    )`;

    try {
      await sql`INSERT INTO ${sql.unsafe(childTable)} VALUES (1, 999)`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.foreignKeyViolation);
    } finally {
      await sql`DROP TABLE ${sql.unsafe(childTable)}`;
      await sql`DROP TABLE ${sql.unsafe(parentTable)}`;
    }
  });

  test("invalid text representation", async () => {
    try {
      await sql`SELECT 'not_a_number'::integer`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.invalidTextRepresentation);
    }
  });

  test("numeric value out of range", async () => {
    try {
      await sql`SELECT 99999::smallint`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.numericValueOutOfRange);
    }
  });

  test("error contains code property", async () => {
    try {
      await sql`INVALID`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
      expect(typeof (e as NeonDbError).code).toBe("string");
    }
  });

  test("error contains message property", async () => {
    try {
      await sql`INVALID`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as Error).message).toBeDefined();
      expect(typeof (e as Error).message).toBe("string");
    }
  });

  test("NeonDbError type", async () => {
    try {
      await sql`INVALID`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).name).toBe("NeonDbError");
    }
  });

  test("consecutive queries after error", async () => {
    try {
      await sql`INVALID`;
    } catch (_e) {
      // ignore
    }

    const result = await sql`SELECT 1 as num`;
    expect(result).toEqual([{ num: 1 }]);
  });

  test("string truncation for char type", async () => {
    const result = await sql`SELECT ${"toolong"}::char(3) as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBe("too");
  });

  test("invalid date format", async () => {
    try {
      await sql`SELECT 'not-a-date'::date`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe("22007");
    }
  });

  test("invalid JSON", async () => {
    try {
      await sql`SELECT '{invalid json}'::jsonb`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("undefined function", async () => {
    try {
      await sql`SELECT nonexistent_function_xyz()`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("error in subquery", async () => {
    try {
      await sql`SELECT * FROM (SELECT 1/0) sub`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.divisionByZero);
    }
  });

  test("error in CTE", async () => {
    try {
      await sql`WITH cte AS (SELECT 1/0 as val) SELECT * FROM cte`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.divisionByZero);
    }
  });

  test("error in CASE expression", async () => {
    try {
      await sql`SELECT CASE WHEN true THEN 1/0 END`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.divisionByZero);
    }
  });

  test("duplicate table error", async () => {
    const tmpTable = uniqueTableName("dup_table");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`;

    try {
      await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (id INT)`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.duplicateTable);
    } finally {
      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    }
  });

  test("duplicate column names allowed", async () => {
    const result = await sql`SELECT 1 as col, 2 as col`;
    const rows = asRows(result);
    expect(rows.length).toBe(1);
  });

  test("invalid array subscript", async () => {
    const result = await sql`SELECT (ARRAY[1,2,3])[0] as val`;
    const rows = asRows(result);
    expect(rows[0].val).toBeNull();
  });

  test("invalid regular expression", async () => {
    try {
      await sql`SELECT 'test' ~ '['`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("empty string as table name", async () => {
    try {
      await sql`SELECT * FROM ""`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("long identifier truncation", async () => {
    const longName = "a".repeat(100);
    const result = await sql`SELECT 1 as ${sql.unsafe(longName)}`;
    const rows = asRows(result);
    expect(rows.length).toBe(1);
  });

  test("recursive CTE without RECURSIVE keyword", async () => {
    try {
      await sql`WITH t AS (SELECT 1 UNION ALL SELECT * FROM t) SELECT * FROM t`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("aggregate function without GROUP BY", async () => {
    try {
      await sql`SELECT id, COUNT(*) FROM ${sql.unsafe(testTable)}`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("window function in WHERE clause", async () => {
    try {
      await sql`SELECT * FROM ${sql.unsafe(testTable)} WHERE row_number() OVER () > 1`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("UPDATE without SET", async () => {
    try {
      await sql`UPDATE ${sql.unsafe(testTable)}`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.syntaxError);
    }
  });

  test("INSERT without VALUES", async () => {
    try {
      await sql`INSERT INTO ${sql.unsafe(testTable)}`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.syntaxError);
    }
  });

  test("GROUP BY with ungrouped column", async () => {
    await sql`INSERT INTO ${sql.unsafe(testTable)} (text_col, int_col) VALUES ('a', 1), ('b', 2)`;
    try {
      await sql`SELECT text_col, int_col, COUNT(*) FROM ${sql.unsafe(testTable)} GROUP BY text_col`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBeDefined();
    }
  });

  test("invalid escape sequence", async () => {
    try {
      await sql`SELECT E'\\x'`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as Error).message).toBeDefined();
    }
  });

  test("division by zero in aggregate", async () => {
    const tmpTable = uniqueTableName("div_zero_agg");
    await sql`CREATE TABLE ${sql.unsafe(tmpTable)} (val INT)`;
    await sql`INSERT INTO ${sql.unsafe(tmpTable)} VALUES (0)`;

    try {
      await sql`SELECT 1/SUM(val) FROM ${sql.unsafe(tmpTable)}`;
      expect(true).toBe(false);
    } catch (e: unknown) {
      expect((e as NeonDbError).code).toBe(pgErrorCodes.divisionByZero);
    } finally {
      await sql`DROP TABLE ${sql.unsafe(tmpTable)}`;
    }
  });
});

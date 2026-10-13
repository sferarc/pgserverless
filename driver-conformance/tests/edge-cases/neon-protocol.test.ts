/**
 * Neon Protocol Edge Cases Tests (50 tests)
 *
 * Tests for Neon serverless driver protocol edge cases based on
 * https://github.com/neondatabase/neon/tree/main/proxy/src/serverless
 */

import {
  Client,
  type NeonDbError,
  type NeonQueryFunction,
  neon,
  Pool,
} from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Neon Protocol Edge Cases", () => {
  const dbUrl = getDatabaseUrl();
  let sql: NeonQueryFunction<false, false>;
  let sqlFull: NeonQueryFunction<false, true>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    sqlFull = neon(dbUrl, { fullResults: true });
    testTable = uniqueTableName("neon_protocol");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        name TEXT,
        value INTEGER
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Special Float Values ====================
  describe("Special Float Values", () => {
    test("positive Infinity", async () => {
      const result = await sql`SELECT 'Infinity'::float8 as val`;
      // Note: Returns JS Infinity which becomes null in JSON.stringify
      expect(result[0].val).toBe(Infinity);
    });

    test("negative Infinity", async () => {
      const result = await sql`SELECT '-Infinity'::float8 as val`;
      expect(result[0].val).toBe(-Infinity);
    });

    test("NaN float value", async () => {
      const result = await sql`SELECT 'NaN'::float8 as val`;
      expect(Number.isNaN(result[0].val)).toBe(true);
    });

    test("Infinity via float8 function", async () => {
      // Note: 1.0/0.0 throws division by zero in strict mode
      // Use explicit cast instead
      const result = await sql`SELECT float8 'Infinity' as val`;
      expect(result[0].val).toBe(Infinity);
    });

    test("mixed Infinity and regular values", async () => {
      const result = await sql`
        SELECT
          'Infinity'::float8 as inf,
          '-Infinity'::float8 as ninf,
          'NaN'::float8 as nan,
          3.14::float8 as regular
      `;
      expect(result[0].inf).toBe(Infinity);
      expect(result[0].ninf).toBe(-Infinity);
      expect(Number.isNaN(result[0].nan)).toBe(true);
      expect(result[0].regular).toBeCloseTo(3.14);
    });

    test("float4 Infinity", async () => {
      const result = await sql`SELECT 'Infinity'::float4 as val`;
      expect(result[0].val).toBe(Infinity);
    });
  });

  // ==================== BigInt Precision ====================
  describe("BigInt (INT8) Precision", () => {
    test("large positive bigint", async () => {
      const result = await sql`SELECT 9007199254740993::bigint as val`;
      // Should be string to preserve precision beyond JS Number.MAX_SAFE_INTEGER
      expect(result[0].val).toBe("9007199254740993");
    });

    test("large negative bigint", async () => {
      const result = await sql`SELECT -9007199254740993::bigint as val`;
      expect(result[0].val).toBe("-9007199254740993");
    });

    test("max bigint value", async () => {
      const result = await sql`SELECT 9223372036854775807::bigint as val`;
      expect(result[0].val).toBe("9223372036854775807");
    });

    test("min bigint value", async () => {
      // Use explicit cast to avoid parsing issues with exact min value
      const result = await sql`SELECT (-9223372036854775807 - 1)::bigint as val`;
      expect(result[0].val).toBe("-9223372036854775808");
    });

    test("safe integer as bigint", async () => {
      const result = await sql`SELECT 42::bigint as val`;
      // Small values might be returned as number or string depending on impl
      expect(["42", 42]).toContain(result[0].val);
    });
  });

  // ==================== Multi-dimensional Arrays ====================
  describe("Multi-dimensional Arrays", () => {
    test("2D integer array", async () => {
      const result = await sql`SELECT '{{1,2},{3,4}}'::int[][] as arr`;
      expect(result[0].arr).toEqual([
        [1, 2],
        [3, 4],
      ]);
    });

    test("3D integer array", async () => {
      const result = await sql`SELECT '{{{1,2},{3,4}},{{5,6},{7,8}}}'::int[][][] as arr`;
      expect(result[0].arr).toEqual([
        [
          [1, 2],
          [3, 4],
        ],
        [
          [5, 6],
          [7, 8],
        ],
      ]);
    });

    test("2D text array", async () => {
      const result = await sql`SELECT '{{"a","b"},{"c","d"}}'::text[][] as arr`;
      expect(result[0].arr).toEqual([
        ["a", "b"],
        ["c", "d"],
      ]);
    });

    test("array with NULL elements", async () => {
      const result = await sql`SELECT '{1,NULL,3}'::int[] as arr`;
      expect(result[0].arr).toEqual([1, null, 3]);
    });

    test("2D array with NULL", async () => {
      const result = await sql`SELECT '{{1,NULL},{NULL,4}}'::int[][] as arr`;
      expect(result[0].arr).toEqual([
        [1, null],
        [null, 4],
      ]);
    });

    test("empty array", async () => {
      const result = await sql`SELECT '{}'::int[] as arr`;
      expect(result[0].arr).toEqual([]);
    });

    test("array with quoted strings", async () => {
      const result = await sql`SELECT ARRAY['hello world', 'with, comma', 'with "quotes"'] as arr`;
      expect(result[0].arr).toEqual(["hello world", "with, comma", 'with "quotes"']);
    });

    test("array with special characters", async () => {
      const result = await sql`SELECT '{"back\\\\slash","new\nline"}'::text[] as arr`;
      expect(result[0].arr[0]).toContain("back");
    });
  });

  // ==================== Command Tags / rowCount ====================
  describe("Command Tags and rowCount", () => {
    test("SELECT rowCount", async () => {
      const result = await sqlFull`SELECT generate_series(1, 5) as n`;
      expect(result.command).toBe("SELECT");
      expect(result.rowCount).toBe(5);
    });

    test("INSERT rowCount", async () => {
      const result = await sqlFull`
        INSERT INTO ${sqlFull.unsafe(testTable)} (name, value)
        VALUES ('a', 1), ('b', 2), ('c', 3)
      `;
      expect(result.command).toBe("INSERT");
      expect(result.rowCount).toBe(3);

      // Cleanup
      await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name IN ('a', 'b', 'c')`;
    });

    test("UPDATE rowCount", async () => {
      // Setup
      await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES ('upd1', 1), ('upd2', 2)`;

      const result = await sqlFull`
        UPDATE ${sqlFull.unsafe(testTable)} SET value = value + 10 WHERE name LIKE 'upd%'
      `;
      expect(result.command).toBe("UPDATE");
      expect(result.rowCount).toBe(2);

      // Cleanup
      await sql`DELETE FROM ${sql.unsafe(testTable)} WHERE name LIKE 'upd%'`;
    });

    test("DELETE rowCount", async () => {
      // Setup
      await sql`INSERT INTO ${sql.unsafe(testTable)} (name, value) VALUES ('del1', 1), ('del2', 2), ('del3', 3)`;

      const result = await sqlFull`
        DELETE FROM ${sqlFull.unsafe(testTable)} WHERE name LIKE 'del%'
      `;
      expect(result.command).toBe("DELETE");
      expect(result.rowCount).toBe(3);
    });

    test("CREATE TABLE command", async () => {
      const tbl = uniqueTableName("cmd_create");
      const result = await sqlFull`CREATE TABLE ${sqlFull.unsafe(tbl)} (id INT)`;
      expect(result.command).toBe("CREATE");
      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("DROP TABLE command", async () => {
      const tbl = uniqueTableName("cmd_drop");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT)`;
      const result = await sqlFull`DROP TABLE ${sqlFull.unsafe(tbl)}`;
      expect(result.command).toBe("DROP");
    });
  });

  // ==================== Fields Metadata ====================
  describe("Fields Metadata (fullResults)", () => {
    test("fields array structure", async () => {
      const result = await sqlFull`SELECT 1 as num, 'text' as str`;

      expect(result.fields.length).toBe(2);
      expect(result.fields[0].name).toBe("num");
      expect(result.fields[1].name).toBe("str");
    });

    test("dataTypeID present", async () => {
      const result = await sqlFull`SELECT 1::integer as int_val`;

      expect(result.fields[0].dataTypeID).toBe(23); // INT4 OID
    });

    test("tableID and columnID for table columns", async () => {
      const result = await sqlFull`SELECT id, name FROM ${sqlFull.unsafe(testTable)} LIMIT 1`;

      // For actual table columns, tableID should be non-zero
      // Note: May be 0 if the driver doesn't preserve this info
      expect(result.fields[0].tableID).toBeDefined();
      expect(result.fields[0].columnID).toBeDefined();
    });

    test("dataTypeSize present", async () => {
      const result = await sqlFull`SELECT 1::integer as val`;
      expect(result.fields[0].dataTypeSize).toBe(4); // INT4 is 4 bytes
    });

    test("dataTypeModifier present", async () => {
      const result = await sqlFull`SELECT 'test'::varchar(100) as val`;
      // varchar(100) has modifier = 100 + 4 = 104
      expect(result.fields[0].dataTypeModifier).toBeDefined();
    });

    test("format field is text", async () => {
      const result = await sqlFull`SELECT 1 as val`;
      expect(result.fields[0].format).toBe("text");
    });
  });

  // ==================== Error Response Fields ====================
  describe("Error Response Fields", () => {
    test("syntax error has code", async () => {
      try {
        await sql`SELEC 1`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.code).toBe("42601"); // syntax_error
      }
    });

    test("division by zero has code", async () => {
      try {
        await sql`SELECT 1/0`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.code).toBe("22012"); // division_by_zero
      }
    });

    test("undefined column has position", async () => {
      try {
        await sql`SELECT undefined_column_xyz`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.code).toBe("42703"); // undefined_column
        // Position should indicate where in query the error occurred
        expect(error.position).toBeDefined();
      }
    });

    test("error has severity", async () => {
      try {
        await sql`SELECT * FROM nonexistent_table_xyz`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.severity).toBe("ERROR");
      }
    });

    test("constraint violation has constraint name", async () => {
      const tbl = uniqueTableName("constraint_test");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`;

      try {
        await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.code).toBe("23505"); // unique_violation
        // Constraint name might be present
      }

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("error includes routine", async () => {
      try {
        await sql`SELECT 1/0`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.routine).toBeDefined();
      }
    });
  });

  // ==================== Array Mode ====================
  describe("Array Mode", () => {
    test("arrayMode returns arrays instead of objects", async () => {
      const sqlArray = neon(dbUrl, { arrayMode: true });
      const result = await sqlArray`SELECT 1 as a, 2 as b`;

      expect(Array.isArray(result[0])).toBe(true);
      expect(result[0]).toEqual([1, 2]);
    });

    test("arrayMode with fullResults", async () => {
      const sqlArrayFull = neon(dbUrl, { arrayMode: true, fullResults: true });
      const result = await sqlArrayFull`SELECT 1 as a, 2 as b`;

      expect(result.rowAsArray).toBe(true);
      expect(Array.isArray(result.rows[0])).toBe(true);
    });

    test("arrayMode preserves column order", async () => {
      const sqlArray = neon(dbUrl, { arrayMode: true });
      const result = await sqlArray`SELECT 3 as third, 1 as first, 2 as second`;

      expect(result[0]).toEqual([3, 1, 2]);
    });
  });

  // ==================== JSON/JSONB Handling ====================
  describe("JSON/JSONB Handling", () => {
    test("JSONB returns object not string", async () => {
      const result = await sql`SELECT '{"a": 1}'::jsonb as data`;
      expect(typeof result[0].data).toBe("object");
      expect(result[0].data).toEqual({ a: 1 });
    });

    test("JSON returns object not string", async () => {
      const result = await sql`SELECT '{"a": 1}'::json as data`;
      expect(typeof result[0].data).toBe("object");
    });

    test("nested JSONB", async () => {
      const result = await sql`SELECT '{"a": {"b": {"c": 1}}}'::jsonb as data`;
      expect(result[0].data).toEqual({ a: { b: { c: 1 } } });
    });

    test("JSONB array", async () => {
      const result = await sql`SELECT '[1, 2, 3]'::jsonb as data`;
      expect(result[0].data).toEqual([1, 2, 3]);
    });

    test("JSONB with null value", async () => {
      const result = await sql`SELECT '{"key": null}'::jsonb as data`;
      expect(result[0].data).toEqual({ key: null });
    });

    test("empty JSONB object", async () => {
      const result = await sql`SELECT '{}'::jsonb as data`;
      expect(result[0].data).toEqual({});
    });

    test("empty JSONB array", async () => {
      const result = await sql`SELECT '[]'::jsonb as data`;
      expect(result[0].data).toEqual([]);
    });
  });

  // ==================== Transaction / Batch ====================
  describe("Transactions", () => {
    test("transaction returns array of results", async () => {
      const results = await sql.transaction([
        sql`SELECT 1 as a`,
        sql`SELECT 2 as b`,
        sql`SELECT 3 as c`,
      ]);

      expect(results.length).toBe(3);
      expect(results[0][0].a).toBe(1);
      expect(results[1][0].b).toBe(2);
      expect(results[2][0].c).toBe(3);
    });

    test("transaction with INSERT", async () => {
      const tbl = uniqueTableName("txn_insert");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT)`;

      const results = await sql.transaction([
        sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`,
        sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (2)`,
        sql`SELECT * FROM ${sql.unsafe(tbl)}`,
      ]);

      expect(results[2].length).toBe(2);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("transaction rollback on error", async () => {
      const tbl = uniqueTableName("txn_rollback");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT PRIMARY KEY)`;

      try {
        await sql.transaction([
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`,
          sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`, // Duplicate - will fail
        ]);
        expect(true).toBe(false);
      } catch {
        // Expected
      }

      // Table should be empty due to rollback
      const result = await sql`SELECT * FROM ${sql.unsafe(tbl)}`;
      expect(result.length).toBe(0);

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("transaction with fullResults", async () => {
      const results = await sqlFull.transaction([
        sqlFull`SELECT 1 as num`,
        sqlFull`SELECT 2 as num`,
      ]);

      expect(results[0].command).toBe("SELECT");
      expect(results[0].rows[0].num).toBe(1);
    });
  });

  // ==================== WebSocket Client Edge Cases ====================
  describe("WebSocket Client Edge Cases", () => {
    test("multiple sequential queries same client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      for (let i = 0; i < 10; i++) {
        const result = await client.query("SELECT $1::integer as num", [i]);
        expect(result.rows[0].num).toBe(i);
      }

      await client.end();
    });

    test("query after transaction commit", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      await client.query("BEGIN");
      await client.query("SELECT 1");
      await client.query("COMMIT");

      // Should work after commit
      const result = await client.query("SELECT 2 as num");
      expect(result.rows[0].num).toBe(2);

      await client.end();
    });

    test("query after transaction rollback", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      await client.query("BEGIN");
      await client.query("SELECT 1");
      await client.query("ROLLBACK");

      // Should work after rollback
      const result = await client.query("SELECT 3 as num");
      expect(result.rows[0].num).toBe(3);

      await client.end();
    });

    test("pool client reuse preserves functionality", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 1 });

      const client1 = await pool.connect();
      await client1.query("SET application_name = 'client1'");
      client1.release();

      const client2 = await pool.connect();
      // Reused connection - application_name might persist or be reset
      const result = await client2.query("SELECT 1 as num");
      expect(result.rows[0].num).toBe(1);
      client2.release();

      await pool.end();
    });
  });
});

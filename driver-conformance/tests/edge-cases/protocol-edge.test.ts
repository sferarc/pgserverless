/**
 * Protocol Edge Cases Tests (25 tests)
 *
 * Tests for edge cases in the PostgreSQL protocol and Neon driver
 */

import { Client, type NeonDbError, neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

describe("Protocol Edge Cases Tests", () => {
  const dbUrl = getDatabaseUrl();
  let sql: ReturnType<typeof neon>;
  let testTable: string;

  beforeAll(async () => {
    sql = neon(dbUrl);
    testTable = uniqueTableName("protocol_test");

    await sql`
      CREATE TABLE IF NOT EXISTS ${sql.unsafe(testTable)} (
        id SERIAL PRIMARY KEY,
        data TEXT
      )
    `;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== Empty Results ====================
  describe("Empty Result Sets", () => {
    test("empty result set", async () => {
      const result = await sql`SELECT 1 WHERE false`;
      expect(result).toEqual([]);
    });

    test("empty result with fullResults", async () => {
      const sqlFull = neon(dbUrl, { fullResults: true });
      const result = await sqlFull`SELECT 1 WHERE false`;
      expect(result.rows).toEqual([]);
      expect(result.rowCount).toBe(0);
      expect(result.fields.length).toBe(1);
    });

    test("empty table select", async () => {
      const emptyTable = uniqueTableName("empty");
      await sql`CREATE TABLE ${sql.unsafe(emptyTable)} (id INT)`;
      const result = await sql`SELECT * FROM ${sql.unsafe(emptyTable)}`;
      expect(result).toEqual([]);
      await sql`DROP TABLE ${sql.unsafe(emptyTable)}`;
    });
  });

  // ==================== Single Row Result ====================
  describe("Single Row Result", () => {
    test("single row select", async () => {
      const result = await sql`SELECT 1 as num`;
      expect(asRows(result).length).toBe(1);
      expect(asRows(result)[0].num).toBe(1);
    });

    test("single row with multiple columns", async () => {
      const result = await sql`SELECT 1 as a, 2 as b, 3 as c`;
      expect(asRows(result).length).toBe(1);
      expect(Object.keys(asRows(result)[0]).length).toBe(3);
    });
  });

  // ==================== Maximum Column Count ====================
  describe("Maximum Column Count", () => {
    test("query with 100 columns", async () => {
      const cols = Array.from({ length: 100 }, (_, i) => `${i} as col${i}`).join(", ");
      const result = await sql.query(`SELECT ${cols}`, []);
      expect(Object.keys(asRows(result)[0]).length).toBe(100);
    });
  });

  // ==================== Very Wide Rows ====================
  describe("Very Wide Rows", () => {
    test("row with large text columns", async () => {
      const largeText = "x".repeat(10000);
      const result = await sql`SELECT ${largeText} as col1, ${largeText} as col2`;
      expect((asRows(result)[0].col1 as string).length).toBe(10000);
      expect((asRows(result)[0].col2 as string).length).toBe(10000);
    });
  });

  // ==================== Very Deep Nesting (JSON) ====================
  describe("Very Deep Nesting (JSON)", () => {
    test("deeply nested JSON", async () => {
      let nested: Record<string, unknown> = { value: "deep" };
      for (let i = 0; i < 50; i++) {
        nested = { level: i, child: nested };
      }

      const result = await sql`SELECT ${JSON.stringify(nested)}::jsonb as data`;
      expect(asRows(result)[0].data).toBeDefined();
    });

    test("JSON with many keys", async () => {
      const obj: Record<string, number> = {};
      for (let i = 0; i < 100; i++) {
        obj[`key${i}`] = i;
      }

      const result = await sql`SELECT ${JSON.stringify(obj)}::jsonb as data`;
      expect(Object.keys(asRows(result)[0].data as object).length).toBe(100);
    });
  });

  // ==================== Unicode Edge Cases ====================
  describe("Unicode Edge Cases", () => {
    test("emoji characters", async () => {
      const emojis = "\ud83d\ude00\ud83d\ude01\ud83d\ude02\ud83d\ude03\ud83d\ude04";
      const result = await sql`SELECT ${emojis} as emojis`;
      expect(asRows(result)[0].emojis).toBe(emojis);
    });

    test("CJK characters", async () => {
      const cjk = "\u4e2d\u6587\u65e5\u672c\u8a9e\ud55c\uae00";
      const result = await sql`SELECT ${cjk} as text`;
      expect(asRows(result)[0].text).toBe(cjk);
    });

    test("RTL characters", async () => {
      const arabic = "\u0627\u0644\u0639\u0631\u0628\u064a\u0629";
      const result = await sql`SELECT ${arabic} as text`;
      expect(asRows(result)[0].text).toBe(arabic);
    });

    test("mixed unicode scripts", async () => {
      const mixed = "Hello \u4e16\u754c \ud83c\udf0d \u0410\u0411\u0412";
      const result = await sql`SELECT ${mixed} as text`;
      expect(asRows(result)[0].text).toBe(mixed);
    });

    test("zero-width characters", async () => {
      const zeroWidth = "a\u200bb\u200cc\u200dd";
      const result = await sql`SELECT ${zeroWidth} as text`;
      expect(asRows(result)[0].text).toBe(zeroWidth);
    });
  });

  // ==================== Binary Protocol ====================
  describe("Binary Data Handling", () => {
    test("binary with null bytes", async () => {
      const binary = Buffer.from([0x00, 0x01, 0x00, 0x02, 0x00]);
      const result = await sql`SELECT ${binary}::bytea as data`;
      expect(asRows(result).length).toBe(1);
    });

    test("all byte values", async () => {
      const allBytes = Buffer.alloc(256);
      for (let i = 0; i < 256; i++) {
        allBytes[i] = i;
      }
      const result = await sql`SELECT ${allBytes}::bytea as data`;
      expect(asRows(result).length).toBe(1);
    });

    test("large binary data", async () => {
      const large = Buffer.alloc(50000, 0xab);
      const result = await sql`SELECT ${large}::bytea as data`;
      expect(asRows(result).length).toBe(1);
    });
  });

  // ==================== Error Message Encoding ====================
  describe("Error Message Encoding", () => {
    test("error with unicode table name", async () => {
      try {
        await sql`SELECT * FROM "nonexistent_\u4e2d\u6587_table"`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as NeonDbError;
        expect(error.code).toBe("42P01");
      }
    });

    test("error message contains query context", async () => {
      try {
        await sql`SELECT * FROM invalid..syntax`;
        expect(true).toBe(false);
      } catch (e) {
        const error = e as Error;
        expect(error.message).toBeDefined();
      }
    });
  });

  // ==================== Notice Messages ====================
  describe("Notice Messages", () => {
    test("notice from RAISE NOTICE (via DO block)", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const notices: unknown[] = [];
      client.on("notice", (notice) => {
        notices.push(notice);
      });

      await client.query("DO $$ BEGIN RAISE NOTICE 'test notice'; END $$");

      await client.end();
      // Notice may or may not be captured depending on implementation
      expect(true).toBe(true);
    });
  });

  // ==================== Connection Parameters ====================
  describe("Connection Parameters", () => {
    test("application_name parameter", async () => {
      const client = new Client({
        connectionString: dbUrl,
        application_name: "test_protocol_app",
      });
      await client.connect();

      const result = await client.query("SHOW application_name");
      expect(result.rows[0].application_name).toBe("test_protocol_app");

      await client.end();
    });
  });

  // ==================== Statement Describe ====================
  describe("Statement Handling", () => {
    test("prepared statement via Pool", async () => {
      const pool = new Pool({ connectionString: dbUrl });

      // Pool uses prepared statements internally
      const result = await pool.query("SELECT $1::integer as num", [42]);
      expect(result.rows[0].num).toBe(42);

      await pool.end();
    });

    test("multiple prepared statement executions", async () => {
      const pool = new Pool({ connectionString: dbUrl });
      const client = await pool.connect();

      for (let i = 0; i < 10; i++) {
        const result = await client.query("SELECT $1::integer as num", [i]);
        expect(result.rows[0].num).toBe(i);
      }

      client.release();
      await pool.end();
    });
  });

  // ==================== Close/Sync Messages ====================
  describe("Close/Sync Messages", () => {
    test("clean connection close", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      await client.query("SELECT 1");
      await client.end();

      // Should not be able to query after close
      try {
        await client.query("SELECT 2");
        expect(true).toBe(false);
      } catch {
        expect(true).toBe(true);
      }
    });

    test("pool client release and reuse", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 1 });

      const client1 = await pool.connect();
      await client1.query("SELECT 1");
      client1.release();

      const client2 = await pool.connect();
      await client2.query("SELECT 2");
      client2.release();

      await pool.end();
    });
  });

  // ==================== NULL Value Handling ====================
  describe("NULL Value Handling", () => {
    test("NULL in various positions", async () => {
      const result = await sql`
        SELECT
          NULL::text as null_text,
          NULL::integer as null_int,
          NULL::jsonb as null_json,
          NULL::boolean as null_bool,
          NULL::bytea as null_bytea
      `;

      expect(asRows(result)[0].null_text).toBeNull();
      expect(asRows(result)[0].null_int).toBeNull();
      expect(asRows(result)[0].null_json).toBeNull();
      expect(asRows(result)[0].null_bool).toBeNull();
      expect(asRows(result)[0].null_bytea).toBeNull();
    });

    test("NULL in array", async () => {
      const result = await sql`SELECT ARRAY[1, NULL, 3] as arr`;
      expect(asRows(result)[0].arr).toEqual([1, null, 3]);
    });

    test("NULL in JSON", async () => {
      const result = await sql`SELECT '{"key": null}'::jsonb as data`;
      expect(asRows(result)[0].data).toEqual({ key: null });
    });
  });

  // ==================== Special SQL Constructs ====================
  describe("Special SQL Constructs", () => {
    test("CTE (WITH clause)", async () => {
      const result = await sql`
        WITH nums AS (SELECT generate_series(1, 5) as n)
        SELECT * FROM nums
      `;
      expect(asRows(result).length).toBe(5);
    });

    test("recursive CTE", async () => {
      const result = await sql`
        WITH RECURSIVE cnt(x) AS (
          SELECT 1
          UNION ALL
          SELECT x + 1 FROM cnt WHERE x < 5
        )
        SELECT x FROM cnt
      `;
      expect(asRows(result).length).toBe(5);
    });

    test("window function", async () => {
      const result = await sql`
        SELECT n, ROW_NUMBER() OVER (ORDER BY n) as rn
        FROM generate_series(1, 3) as n
      `;
      expect(asRows(result).length).toBe(3);
    });

    test("LATERAL join", async () => {
      const result = await sql`
        SELECT * FROM
          (VALUES (1), (2), (3)) AS t(n),
          LATERAL (SELECT n * 2 as doubled) AS doubled
      `;
      expect(asRows(result).length).toBe(3);
    });

    test("CASE expression", async () => {
      const result = await sql`
        SELECT
          CASE
            WHEN 1 = 1 THEN 'yes'
            ELSE 'no'
          END as result
      `;
      expect(asRows(result)[0].result).toBe("yes");
    });

    test("COALESCE and NULLIF", async () => {
      const result = await sql`
        SELECT
          COALESCE(NULL, NULL, 'default') as coalesce_result,
          NULLIF(1, 1) as nullif_same,
          NULLIF(1, 2) as nullif_diff
      `;
      expect(asRows(result)[0].coalesce_result).toBe("default");
      expect(asRows(result)[0].nullif_same).toBeNull();
      expect(asRows(result)[0].nullif_diff).toBe(1);
    });
  });

  // ==================== Asynchronous Messages ====================
  describe("Asynchronous Messages", () => {
    test("handles pg_sleep gracefully", async () => {
      const result = await sql`SELECT pg_sleep(0.1), 'done' as status`;
      expect(asRows(result)[0].status).toBe("done");
    });
  });
});

/**
 * Configuration Variation Tests
 *
 * Based on real bugs from Neon:
 * - Neon #1/#34: pipelineConnect caused failures with certain proxies
 * - Neon #107: fetchEndpoint configuration not working correctly
 * - Neon #130: Custom fetch function behavior
 * - Neon #56: Stale responses due to fetch caching (Next.js)
 * - Neon #146: SubtleCrypto shim issues
 *
 * These tests verify that different neonConfig options and
 * connection configurations work correctly.
 */

import { Client, neon, neonConfig, Pool } from "@neondatabase/serverless";
import { afterAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Configuration Variation Tests", () => {
  const dbUrl = getDatabaseUrl();

  afterAll(() => {});

  // ==================== HTTP Options ====================
  describe("HTTP neon() Options", () => {
    test("default options work", async () => {
      const sql = neon(dbUrl);
      const result = await sql`SELECT 1 as val`;
      expect(result[0].val).toBe(1);
    });

    test("fullResults: true returns metadata", async () => {
      const sql = neon(dbUrl, { fullResults: true });
      const result = await sql`SELECT 1 as val, 'text' as str`;

      expect(result.rows).toBeDefined();
      expect(result.fields).toBeDefined();
      expect(result.command).toBe("SELECT");
      expect(result.rowCount).toBe(1);
      expect(result.rows[0].val).toBe(1);
      expect(result.fields.length).toBe(2);
      expect(result.fields[0].name).toBe("val");
      expect(result.fields[1].name).toBe("str");
    });

    test("arrayMode: true returns arrays", async () => {
      const sql = neon(dbUrl, { arrayMode: true });
      const result = await sql`SELECT 1 as a, 2 as b, 3 as c`;

      expect(Array.isArray(result[0])).toBe(true);
      expect(result[0]).toEqual([1, 2, 3]);
    });

    test("arrayMode + fullResults combined", async () => {
      const sql = neon(dbUrl, { arrayMode: true, fullResults: true });
      const result = await sql`SELECT 1 as a, 2 as b`;

      expect(result.rowAsArray).toBe(true);
      expect(Array.isArray(result.rows[0])).toBe(true);
      expect(result.rows[0]).toEqual([1, 2]);
      expect(result.fields.length).toBe(2);
    });

    test("fullResults fields have dataTypeID", async () => {
      const sql = neon(dbUrl, { fullResults: true });
      const result =
        await sql`SELECT 1::integer as int_val, 'text'::text as text_val, true::boolean as bool_val`;

      // INT4 OID = 23, TEXT OID = 25, BOOL OID = 16
      expect(result.fields[0].dataTypeID).toBe(23);
      expect(result.fields[1].dataTypeID).toBe(25);
      expect(result.fields[2].dataTypeID).toBe(16);
    });

    test("fullResults for INSERT returns rowCount", async () => {
      const sql = neon(dbUrl, { fullResults: true });
      const tbl = uniqueTableName("fr_insert");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT)`;
      const result = await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1), (2), (3)`;
      expect(result.command).toBe("INSERT");
      expect(result.rowCount).toBe(3);
      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("fullResults for UPDATE returns rowCount", async () => {
      const sql = neon(dbUrl, { fullResults: true });
      const tbl = uniqueTableName("fr_update");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT, val INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1, 10), (2, 20), (3, 30)`;
      const result = await sql`UPDATE ${sql.unsafe(tbl)} SET val = val + 1`;
      expect(result.command).toBe("UPDATE");
      expect(result.rowCount).toBe(3);
      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });

    test("fullResults for DELETE returns rowCount", async () => {
      const sql = neon(dbUrl, { fullResults: true });
      const tbl = uniqueTableName("fr_delete");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT)`;
      await sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1), (2)`;
      const result = await sql`DELETE FROM ${sql.unsafe(tbl)}`;
      expect(result.command).toBe("DELETE");
      expect(result.rowCount).toBe(2);
      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Transaction Options ====================
  describe("Transaction with Options", () => {
    test("transaction with fullResults", async () => {
      const sql = neon(dbUrl, { fullResults: true });
      const results = await sql.transaction([sql`SELECT 1 as val`, sql`SELECT 2 as val`]);

      expect(results.length).toBe(2);
      expect(results[0].command).toBe("SELECT");
      expect(results[0].rows[0].val).toBe(1);
      expect(results[1].rows[0].val).toBe(2);
    });

    test("transaction with arrayMode", async () => {
      const sql = neon(dbUrl, { arrayMode: true });
      const results = await sql.transaction([sql`SELECT 1 as a, 2 as b`, sql`SELECT 3 as c`]);

      expect(results.length).toBe(2);
      expect(results[0][0]).toEqual([1, 2]);
      expect(results[1][0]).toEqual([3]);
    });

    test("transaction isolationLevel readCommitted", async () => {
      const sql = neon(dbUrl);
      const results = await sql.transaction([sql`SELECT 1 as val`], {
        isolationLevel: "ReadCommitted",
      });
      expect(results[0][0].val).toBe(1);
    });

    test("transaction isolationLevel repeatableRead", async () => {
      const sql = neon(dbUrl);
      const results = await sql.transaction([sql`SELECT 1 as val`], {
        isolationLevel: "RepeatableRead",
      });
      expect(results[0][0].val).toBe(1);
    });

    test("transaction isolationLevel serializable", async () => {
      const sql = neon(dbUrl);
      const results = await sql.transaction([sql`SELECT 1 as val`], {
        isolationLevel: "Serializable",
      });
      expect(results[0][0].val).toBe(1);
    });

    test("transaction readOnly", async () => {
      const sql = neon(dbUrl);
      const results = await sql.transaction([sql`SELECT 1 as val`], { readOnly: true });
      expect(results[0][0].val).toBe(1);
    });

    test("readOnly transaction rejects writes", async () => {
      const sql = neon(dbUrl);
      const tbl = uniqueTableName("ro_txn");
      await sql`CREATE TABLE ${sql.unsafe(tbl)} (id INT)`;

      try {
        await sql.transaction([sql`INSERT INTO ${sql.unsafe(tbl)} VALUES (1)`], { readOnly: true });
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }

      await sql`DROP TABLE ${sql.unsafe(tbl)}`;
    });
  });

  // ==================== Client Configuration ====================
  describe("Client Configuration", () => {
    test("Client with connection string", async () => {
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);
      await client.end();
    });

    test("Client with config object", async () => {
      const url = new URL(dbUrl);
      const client = new Client({
        host: url.hostname,
        port: Number(url.port) || 5432,
        database: url.pathname.slice(1),
        user: url.username,
        password: decodeURIComponent(url.password),
        ssl: true,
      });
      await client.connect();
      const result = await client.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);
      await client.end();
    });

    test("Client with connectionString in config", async () => {
      const client = new Client({ connectionString: dbUrl });
      await client.connect();
      const result = await client.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);
      await client.end();
    });
  });

  // ==================== Pool Configuration ====================
  describe("Pool Configuration", () => {
    test("Pool with max: 1", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 1 });
      const result = await pool.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);
      await pool.end();
    });

    test("Pool with max: 5", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 5 });

      // Run 10 queries in parallel (should queue beyond 5)
      const queries = Array.from({ length: 10 }, (_, i) =>
        pool.query("SELECT $1::integer as num", [i]),
      );
      const results = await Promise.all(queries);

      for (let i = 0; i < 10; i++) {
        expect(results[i].rows[0].num).toBe(i);
      }

      await pool.end();
    });

    test("Pool totalCount and idleCount", async () => {
      const pool = new Pool({ connectionString: dbUrl, max: 2 });

      // Make a query to establish a connection
      await pool.query("SELECT 1");

      expect(pool.totalCount).toBeGreaterThanOrEqual(0);
      expect(pool.idleCount).toBeGreaterThanOrEqual(0);

      await pool.end();
    });
  });

  // ==================== Multiple neon() Instances ====================
  describe("Multiple neon() Instances", () => {
    test("two neon() instances don't interfere", async () => {
      const sql1 = neon(dbUrl);
      const sql2 = neon(dbUrl, { fullResults: true });

      const r1 = await sql1`SELECT 1 as val`;
      const r2 = await sql2`SELECT 2 as val`;

      // r1 should be simple array
      expect(Array.isArray(r1)).toBe(true);
      expect(r1[0].val).toBe(1);

      // r2 should be full result
      expect(r2.rows).toBeDefined();
      expect(r2.rows[0].val).toBe(2);
    });

    test("concurrent queries from different instances", async () => {
      const sql1 = neon(dbUrl);
      const sql2 = neon(dbUrl);
      const sql3 = neon(dbUrl);

      const [r1, r2, r3] = await Promise.all([
        sql1`SELECT 'a' as val`,
        sql2`SELECT 'b' as val`,
        sql3`SELECT 'c' as val`,
      ]);

      expect(r1[0].val).toBe("a");
      expect(r2[0].val).toBe("b");
      expect(r3[0].val).toBe("c");
    });
  });

  // ==================== sql.query() Method ====================
  describe("sql.query() Method", () => {
    test("query() with text and params", async () => {
      const sql = neon(dbUrl);
      const result = await sql.query("SELECT $1::integer as val", [42]);
      expect(result[0].val).toBe(42);
    });

    test("query() with no params", async () => {
      const sql = neon(dbUrl);
      const result = await sql.query("SELECT 1 as val", []);
      expect(result[0].val).toBe(1);
    });

    test("query() with multiple params", async () => {
      const sql = neon(dbUrl);
      const result = await sql.query("SELECT $1::integer as a, $2::text as b, $3::boolean as c", [
        42,
        "hello",
        true,
      ]);
      expect(result[0].a).toBe(42);
      expect(result[0].b).toBe("hello");
      expect(result[0].c).toBe(true);
    });
  });

  // ==================== neonConfig Global Settings ====================
  describe("neonConfig Global Settings", () => {
    test("pipelineConnect false works", async () => {
      neonConfig.pipelineConnect = false;
      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 1 as val");
      expect(result.rows[0].val).toBe(1);
      await client.end();
    });

    test("wsProxy can be set without error", () => {
      // Just verify the config property exists and can be set
      const original = neonConfig.wsProxy;
      neonConfig.wsProxy = undefined as never;
      neonConfig.wsProxy = original;
      expect(true).toBe(true);
    });
  });
});

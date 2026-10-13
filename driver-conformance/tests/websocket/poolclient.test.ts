/**
 * WebSocket PoolClient Tests (15 tests)
 * Split from connection.test.ts for parallel execution
 */

import { Client, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("WebSocket PoolClient Tests", () => {
  const dbUrl = getDatabaseUrl();
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("ws_poolclient");
    const client = new Client(dbUrl);
    await client.connect();
    await client.query(`
      CREATE TABLE IF NOT EXISTS ${testTable} (
        id SERIAL PRIMARY KEY,
        name TEXT,
        value INTEGER
      )
    `);
    await client.end();
  });

  afterAll(async () => {
    const client = new Client(dbUrl);
    await client.connect();
    await client.query(`DROP TABLE IF EXISTS ${testTable}`);
    await client.end();
  });

  test("PoolClient query", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const result = await client.query("SELECT 1 as num");
    expect(result.rows[0].num).toBe(1);
    client.release();
    await pool.end();
  });

  test("PoolClient release()", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    await client.query("SELECT 1");
    client.release();
    const client2 = await pool.connect();
    expect(client2).toBeDefined();
    client2.release();
    await pool.end();
  });

  test("PoolClient release(true) - destroy", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 1 });
    const client = await pool.connect();
    await client.query("SELECT 1");
    client.release(true);

    const client2 = await pool.connect();
    await client2.query("SELECT 2");
    client2.release();
    await pool.end();
  });

  test("PoolClient transaction", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const name = uniqueTableName("poolclient_txn");

    try {
      await client.query("BEGIN");
      await client.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 1]);
      await client.query("COMMIT");
    } finally {
      client.release();
    }

    const result = await pool.query(`SELECT * FROM ${testTable} WHERE name = $1`, [name]);
    expect(result.rows.length).toBe(1);

    await pool.query(`DELETE FROM ${testTable} WHERE name = $1`, [name]);
    await pool.end();
  });

  test("PoolClient multiple transactions", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();

    await client.query("BEGIN");
    await client.query("SELECT 1");
    await client.query("COMMIT");

    await client.query("BEGIN");
    await client.query("SELECT 2");
    await client.query("COMMIT");

    client.release();
    await pool.end();
  });

  test("PoolClient neonConfig access", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    expect(client.neonConfig).toBeDefined();
    client.release();
    await pool.end();
  });

  test("PoolClient isolation between clients", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client1 = await pool.connect();
    const client2 = await pool.connect();

    await client1.query("SET application_name = 'client1'");
    const r1 = await client1.query("SHOW application_name");
    const r2 = await client2.query("SHOW application_name");

    expect(r1.rows[0].application_name).toBe("client1");
    expect(r2.rows[0].application_name).not.toBe("client1");

    client1.release();
    client2.release();
    await pool.end();
  });

  test("PoolClient error handling", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();

    try {
      await client.query("INVALID SYNTAX");
      expect(true).toBe(false);
    } catch (e) {
      expect(e).toBeDefined();
    }

    const result = await client.query("SELECT 1 as num");
    expect(result.rows[0].num).toBe(1);

    client.release();
    await pool.end();
  });

  test("PoolClient with parameters", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const result = await client.query("SELECT $1::text as val, $2::integer as num", ["hello", 42]);
    expect(result.rows[0]).toEqual({ val: "hello", num: 42 });
    client.release();
    await pool.end();
  });

  test("PoolClient consecutive queries", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();

    const r1 = await client.query("SELECT 1 as n");
    const r2 = await client.query("SELECT 2 as n");
    const r3 = await client.query("SELECT 3 as n");

    expect(r1.rows[0].n).toBe(1);
    expect(r2.rows[0].n).toBe(2);
    expect(r3.rows[0].n).toBe(3);

    client.release();
    await pool.end();
  });

  test("PoolClient with binary data", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const binary = Buffer.from([0xca, 0xfe]);
    const result = await client.query("SELECT $1::bytea as data", [binary]);
    expect(result.rows.length).toBe(1);
    client.release();
    await pool.end();
  });

  test("PoolClient with JSON data", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const json = { nested: { value: true } };
    const result = await client.query("SELECT $1::jsonb as data", [JSON.stringify(json)]);
    expect(result.rows[0].data).toEqual(json);
    client.release();
    await pool.end();
  });

  test("PoolClient with array data", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const result = await client.query("SELECT $1::text[] as items", [["a", "b", "c"]]);
    expect(result.rows[0].items).toEqual(["a", "b", "c"]);
    client.release();
    await pool.end();
  });

  test("PoolClient release in finally block", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();

    try {
      await client.query("SELECT 1");
      throw new Error("Intentional error");
    } catch {
      // Expected
    } finally {
      client.release();
    }

    const result = await pool.query("SELECT 'ok' as status");
    expect(result.rows[0].status).toBe("ok");
    await pool.end();
  });
});

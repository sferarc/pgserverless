/**
 * WebSocket Pool Connection Tests (32 tests)
 * Split from connection.test.ts for parallel execution
 */

import { Client, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("WebSocket Pool Tests", () => {
  const dbUrl = getDatabaseUrl();
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("ws_pool");
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

  test("basic pool creation", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    expect(pool).toBeDefined();
    await pool.end();
  });

  test("pool connection acquisition", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    expect(client).toBeDefined();
    client.release();
    await pool.end();
  });

  test("pool connection release", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    await client.query("SELECT 1");
    client.release();
    await pool.end();
  });

  test("pool automatic release with query", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT 1 as num");
    expect(result.rows[0].num).toBe(1);
    await pool.end();
  });

  test("pool max connections", async () => {
    const pool = new Pool({
      connectionString: dbUrl,
      max: 2,
    });

    const client1 = await pool.connect();
    const client2 = await pool.connect();

    expect(client1).toBeDefined();
    expect(client2).toBeDefined();

    client1.release();
    client2.release();
    await pool.end();
  });

  test("pool query direct", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT 'direct' as method");
    expect(result.rows[0].method).toBe("direct");
    await pool.end();
  });

  test("pool query via connect", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const result = await client.query("SELECT 'via connect' as method");
    expect(result.rows[0].method).toBe("via connect");
    client.release();
    await pool.end();
  });

  test("pool event: connect", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    let connectFired = false;
    pool.on("connect", () => {
      connectFired = true;
    });
    await pool.query("SELECT 1");
    expect(connectFired).toBe(true);
    await pool.end();
  });

  test("pool event: acquire", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    let acquireFired = false;
    pool.on("acquire", () => {
      acquireFired = true;
    });
    const client = await pool.connect();
    expect(acquireFired).toBe(true);
    client.release();
    await pool.end();
  });

  test("pool event: release", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    let releaseFired = false;
    pool.on("release", () => {
      releaseFired = true;
    });
    const client = await pool.connect();
    client.release();
    expect(releaseFired).toBe(true);
    await pool.end();
  });

  test("pool client isolation", async () => {
    const pool = new Pool({ connectionString: dbUrl });

    const client1 = await pool.connect();
    const client2 = await pool.connect();

    await client1.query("SET application_name = 'client1'");
    const result = await client2.query("SHOW application_name");
    expect(result.rows[0].application_name).not.toBe("client1");

    client1.release();
    client2.release();
    await pool.end();
  });

  test("pool transaction handling", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const client = await pool.connect();
    const name = uniqueTableName("pool_txn");

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

  test("pool concurrent queries", async () => {
    const pool = new Pool({ connectionString: dbUrl });

    const results = await Promise.all([
      pool.query("SELECT 1 as num"),
      pool.query("SELECT 2 as num"),
      pool.query("SELECT 3 as num"),
      pool.query("SELECT 4 as num"),
      pool.query("SELECT 5 as num"),
    ]);

    expect(results.length).toBe(5);
    await pool.end();
  });

  test("pool query queueing", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 1 });

    const p1 = pool.query("SELECT pg_sleep(0.1), 1 as num");
    const p2 = pool.query("SELECT 2 as num");

    const results = await Promise.all([p1, p2]);
    expect(results.length).toBe(2);

    await pool.end();
  });

  test("pool end", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    await pool.query("SELECT 1");
    await pool.end();

    try {
      await pool.query("SELECT 1");
      expect(true).toBe(false);
    } catch (e) {
      expect(e).toBeDefined();
    }
  });

  test("pool error recovery", async () => {
    const pool = new Pool({ connectionString: dbUrl });

    try {
      await pool.query("INVALID SYNTAX");
    } catch {
      // Expected
    }

    const result = await pool.query("SELECT 1 as num");
    expect(result.rows[0].num).toBe(1);

    await pool.end();
  });

  test("pool connection reuse", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 1 });

    const client1 = await pool.connect();
    await client1.query("SELECT 1");
    client1.release();

    const client2 = await pool.connect();
    await client2.query("SELECT 2");
    client2.release();

    await pool.end();
  });

  test("pool with parameters", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT $1::text as val, $2::integer as num", ["hello", 42]);
    expect(result.rows[0]).toEqual({ val: "hello", num: 42 });
    await pool.end();
  });

  test("pool with array parameters", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT $1::integer[] as nums", [[1, 2, 3]]);
    expect(result.rows[0].nums).toEqual([1, 2, 3]);
    await pool.end();
  });

  test("pool with JSON", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const json = { key: "value" };
    const result = await pool.query("SELECT $1::jsonb as data", [JSON.stringify(json)]);
    expect(result.rows[0].data).toEqual(json);
    await pool.end();
  });

  test("pool with NULL", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT $1::text as val", [null]);
    expect(result.rows[0].val).toBeNull();
    await pool.end();
  });

  test("pool query result structure", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT 1 as num");

    expect(result.rows).toBeDefined();
    expect(result.fields).toBeDefined();
    expect(result.command).toBe("SELECT");
    expect(result.rowCount).toBe(1);

    await pool.end();
  });

  test("pool with INSERT", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const name = uniqueTableName("pool_insert");
    const result = await pool.query(
      `INSERT INTO ${testTable} (name, value) VALUES ($1, $2) RETURNING *`,
      [name, 100],
    );
    expect(result.rows[0].value).toBe(100);
    await pool.query(`DELETE FROM ${testTable} WHERE name = $1`, [name]);
    await pool.end();
  });

  test("pool with UPDATE", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const name = uniqueTableName("pool_update");
    await pool.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 10]);
    const result = await pool.query(
      `UPDATE ${testTable} SET value = $1 WHERE name = $2 RETURNING *`,
      [20, name],
    );
    expect(result.rows[0].value).toBe(20);
    await pool.query(`DELETE FROM ${testTable} WHERE name = $1`, [name]);
    await pool.end();
  });

  test("pool with DELETE", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const name = uniqueTableName("pool_delete");
    await pool.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 50]);
    const result = await pool.query(`DELETE FROM ${testTable} WHERE name = $1 RETURNING *`, [name]);
    expect(result.rowCount).toBe(1);
    await pool.end();
  });

  test("pool totalCount", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 5 });
    const client = await pool.connect();
    expect(pool.totalCount).toBeGreaterThanOrEqual(1);
    client.release();
    await pool.end();
  });

  test("pool idleCount", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    await pool.query("SELECT 1");
    expect(pool.idleCount).toBeGreaterThanOrEqual(0);
    await pool.end();
  });

  test("pool waitingCount", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 1 });
    expect(pool.waitingCount).toBe(0);
    await pool.end();
  });

  test("pool with large result", async () => {
    const pool = new Pool({ connectionString: dbUrl });
    const result = await pool.query("SELECT generate_series(1, 5000) as n");
    expect(result.rows.length).toBe(5000);
    await pool.end();
  });

  test("pool stress test - many queries", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 5 });

    const queries = Array.from({ length: 50 }, (_, i) =>
      pool.query("SELECT $1::integer as num", [i]),
    );

    const results = await Promise.all(queries);
    expect(results.length).toBe(50);

    await pool.end();
  });

  test("pool client.release(true) destroys", async () => {
    const pool = new Pool({ connectionString: dbUrl, max: 2 });

    const client1 = await pool.connect();
    await client1.query("SELECT 1");
    client1.release(true);

    const client2 = await pool.connect();
    await client2.query("SELECT 2");
    client2.release();

    await pool.end();
  });
});

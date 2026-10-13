/**
 * WebSocket Client Connection Tests (32 tests)
 * Split from connection.test.ts for parallel execution
 */

import { Client } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("WebSocket Client Tests", () => {
  const dbUrl = getDatabaseUrl();
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("ws_client");
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

  test("basic client connection", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT 1 as num");
    expect(result.rows).toEqual([{ num: 1 }]);
    await client.end();
  });

  test("connection with connection string", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    expect(client).toBeDefined();
    await client.end();
  });

  test("connection with config object", async () => {
    const url = new URL(dbUrl);
    const client = new Client({
      host: url.hostname,
      port: parseInt(url.port, 10) || 5432,
      database: url.pathname.slice(1),
      user: url.username,
      password: decodeURIComponent(url.password),
      ssl: true,
    });
    await client.connect();
    const result = await client.query("SELECT 1");
    expect(result.rows.length).toBe(1);
    await client.end();
  });

  test("client query after connect", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT 'hello' as greeting");
    expect(result.rows[0].greeting).toBe("hello");
    await client.end();
  });

  test("client multiple queries", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const r1 = await client.query("SELECT 1 as num");
    const r2 = await client.query("SELECT 2 as num");
    const r3 = await client.query("SELECT 3 as num");
    expect(r1.rows[0].num).toBe(1);
    expect(r2.rows[0].num).toBe(2);
    expect(r3.rows[0].num).toBe(3);
    await client.end();
  });

  test("client parameterized query", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT $1::text as val", ["param"]);
    expect(result.rows[0].val).toBe("param");
    await client.end();
  });

  test("client end()", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    await client.query("SELECT 1");
    await client.end();
    try {
      await client.query("SELECT 1");
      expect(true).toBe(false);
    } catch (e) {
      expect(e).toBeDefined();
    }
  });

  test("client event: end", async () => {
    const client = new Client(dbUrl);
    let endFired = false;
    client.on("end", () => {
      endFired = true;
    });
    await client.connect();
    await client.end();
    expect(endFired).toBe(true);
  });

  test("client event: error handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    try {
      await client.query("INVALID SYNTAX");
    } catch (e) {
      expect(e).toBeDefined();
    }
    await client.end();
  });

  test("client with INSERT", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const name = uniqueTableName("insert");
    const result = await client.query(
      `INSERT INTO ${testTable} (name, value) VALUES ($1, $2) RETURNING *`,
      [name, 42],
    );
    expect(result.rows[0].value).toBe(42);
    await client.query(`DELETE FROM ${testTable} WHERE name = $1`, [name]);
    await client.end();
  });

  test("client with UPDATE", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const name = uniqueTableName("update");
    await client.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 10]);
    const result = await client.query(
      `UPDATE ${testTable} SET value = $1 WHERE name = $2 RETURNING *`,
      [20, name],
    );
    expect(result.rows[0].value).toBe(20);
    await client.query(`DELETE FROM ${testTable} WHERE name = $1`, [name]);
    await client.end();
  });

  test("client with DELETE", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const name = uniqueTableName("delete");
    await client.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 100]);
    const result = await client.query(`DELETE FROM ${testTable} WHERE name = $1 RETURNING *`, [
      name,
    ]);
    expect(result.rowCount).toBe(1);
    await client.end();
  });

  test("client transaction BEGIN/COMMIT", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const name = uniqueTableName("txn");
    await client.query("BEGIN");
    await client.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 1]);
    await client.query("COMMIT");
    const result = await client.query(`SELECT * FROM ${testTable} WHERE name = $1`, [name]);
    expect(result.rows.length).toBe(1);
    await client.query(`DELETE FROM ${testTable} WHERE name = $1`, [name]);
    await client.end();
  });

  test("client transaction ROLLBACK", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const name = uniqueTableName("rollback");
    await client.query("BEGIN");
    await client.query(`INSERT INTO ${testTable} (name, value) VALUES ($1, $2)`, [name, 1]);
    await client.query("ROLLBACK");
    const result = await client.query(`SELECT * FROM ${testTable} WHERE name = $1`, [name]);
    expect(result.rows.length).toBe(0);
    await client.end();
  });

  test("client neonConfig access", async () => {
    const client = new Client(dbUrl);
    expect(client.neonConfig).toBeDefined();
    await client.connect();
    await client.end();
  });

  test("client with binary data", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const binary = Buffer.from([0xde, 0xad, 0xbe, 0xef]);
    const result = await client.query("SELECT $1::bytea as data", [binary]);
    expect(result.rows.length).toBe(1);
    await client.end();
  });

  test("client with JSON data", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const json = { key: "value", nested: { num: 42 } };
    const result = await client.query("SELECT $1::jsonb as data", [JSON.stringify(json)]);
    expect(result.rows[0].data).toEqual(json);
    await client.end();
  });

  test("client with NULL values", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT $1::text as val", [null]);
    expect(result.rows[0].val).toBeNull();
    await client.end();
  });

  test("client with array data", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT $1::integer[] as nums", [[1, 2, 3]]);
    expect(result.rows[0].nums).toEqual([1, 2, 3]);
    await client.end();
  });

  test("client query result fields", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT 1 as num, 'text' as str");
    expect(result.fields.length).toBe(2);
    expect(result.fields[0].name).toBe("num");
    expect(result.fields[1].name).toBe("str");
    await client.end();
  });

  test("client query rowCount", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT generate_series(1, 5) as n");
    expect(result.rowCount).toBe(5);
    await client.end();
  });

  test("client query command", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT 1");
    expect(result.command).toBe("SELECT");
    await client.end();
  });

  test("client concurrent queries", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const results = await Promise.all([
      client.query("SELECT 1 as num"),
      client.query("SELECT 2 as num"),
      client.query("SELECT 3 as num"),
    ]);
    expect(results.length).toBe(3);
    await client.end();
  });

  test("client with large result set", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT generate_series(1, 1000) as n");
    expect(result.rows.length).toBe(1000);
    await client.end();
  });

  test("client with application_name", async () => {
    const client = new Client({
      connectionString: dbUrl,
      application_name: "test_app",
    });
    await client.connect();
    const result = await client.query("SELECT current_setting('application_name') as app");
    expect(result.rows[0].app).toBe("test_app");
    await client.end();
  });

  test("client error recovery - continue after error", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    try {
      await client.query("INVALID SYNTAX");
    } catch {
      // Expected error
    }
    const result = await client.query("SELECT 1 as num");
    expect(result.rows[0].num).toBe(1);
    await client.end();
  });

  test("client with statement_timeout", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    await client.query("SET statement_timeout = '10s'");
    const result = await client.query("SELECT 1");
    expect(result.rows.length).toBe(1);
    await client.end();
  });

  test("client consecutive transactions", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    await client.query("BEGIN");
    await client.query("SELECT 1");
    await client.query("COMMIT");
    await client.query("BEGIN");
    await client.query("SELECT 2");
    await client.query("COMMIT");
    await client.end();
  });

  test("client with different data types", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query(`
      SELECT
        42::integer as int_val,
        3.14::float as float_val,
        'text'::text as text_val,
        true::boolean as bool_val,
        '2024-01-15'::date as date_val
    `);
    expect(result.rows[0].int_val).toBe(42);
    expect(result.rows[0].text_val).toBe("text");
    expect(result.rows[0].bool_val).toBe(true);
    await client.end();
  });
});

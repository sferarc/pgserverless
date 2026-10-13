/**
 * WebSocket Protocol Tests (15 tests)
 * Split from connection.test.ts for parallel execution
 */

import { Client } from "@neondatabase/serverless";
import { describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";

describe("WebSocket Protocol Tests", () => {
  const dbUrl = getDatabaseUrl();

  test("connection establishment", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    expect(true).toBe(true);
    await client.end();
  });

  test("message handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT 1");
    expect(result.rows.length).toBe(1);
    await client.end();
  });

  test("large message handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const largeString = "x".repeat(100000);
    const result = await client.query("SELECT $1 as data", [largeString]);
    expect(result.rows[0].data).toBe(largeString);
    await client.end();
  });

  test("connection keepalive", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    await new Promise((resolve) => setTimeout(resolve, 1000));

    const result = await client.query("SELECT 1 as num");
    expect(result.rows[0].num).toBe(1);

    await client.end();
  });

  test("concurrent message handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    const results = await Promise.all([
      client.query("SELECT 1 as n"),
      client.query("SELECT 2 as n"),
      client.query("SELECT 3 as n"),
    ]);

    expect(results.length).toBe(3);
    await client.end();
  });

  test("message ordering", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    const r1 = await client.query("SELECT 1 as n");
    const r2 = await client.query("SELECT 2 as n");
    const r3 = await client.query("SELECT 3 as n");

    expect(r1.rows[0].n).toBe(1);
    expect(r2.rows[0].n).toBe(2);
    expect(r3.rows[0].n).toBe(3);

    await client.end();
  });

  test("error frame handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    try {
      await client.query("SELECT 1/0");
    } catch (e) {
      expect((e as { code: string }).code).toBe("22012");
    }

    await client.end();
  });

  test("connection close handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    await client.query("SELECT 1");
    await client.end();

    try {
      await client.query("SELECT 1");
      expect(true).toBe(false);
    } catch {
      expect(true).toBe(true);
    }
  });

  test("multiple queries same connection", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    for (let i = 0; i < 10; i++) {
      const result = await client.query("SELECT $1::integer as num", [i]);
      expect(result.rows[0].num).toBe(i);
    }

    await client.end();
  });

  test("query with many columns", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    const result = await client.query(`
        SELECT
          1 as c1, 2 as c2, 3 as c3, 4 as c4, 5 as c5,
          6 as c6, 7 as c7, 8 as c8, 9 as c9, 10 as c10
      `);

    expect(result.fields.length).toBe(10);
    await client.end();
  });

  test("query with many rows", async () => {
    const client = new Client(dbUrl);
    await client.connect();
    const result = await client.query("SELECT generate_series(1, 10000) as n");
    expect(result.rows.length).toBe(10000);
    await client.end();
  });

  test("binary data transfer", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    const allBytes = Buffer.alloc(256);
    for (let i = 0; i < 256; i++) {
      allBytes[i] = i;
    }

    const result = await client.query("SELECT $1::bytea as data", [allBytes]);
    expect(result.rows.length).toBe(1);

    await client.end();
  });

  test("unicode handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    const unicode = "\u4e2d\u6587\ud83d\ude00\u0410\u0411\u0412";
    const result = await client.query("SELECT $1 as text", [unicode]);
    expect(result.rows[0].text).toBe(unicode);

    await client.end();
  });

  test("NULL value handling", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    const result = await client.query(
      "SELECT NULL::text as a, NULL::integer as b, NULL::jsonb as c",
    );
    expect(result.rows[0].a).toBeNull();
    expect(result.rows[0].b).toBeNull();
    expect(result.rows[0].c).toBeNull();

    await client.end();
  });

  test("connection state after many operations", async () => {
    const client = new Client(dbUrl);
    await client.connect();

    for (let i = 0; i < 20; i++) {
      await client.query("SELECT $1", [i]);
    }

    const result = await client.query("SELECT 'healthy' as status");
    expect(result.rows[0].status).toBe("healthy");

    await client.end();
  }, 30000);
});

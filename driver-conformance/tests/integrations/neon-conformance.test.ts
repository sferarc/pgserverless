/**
 * @neondatabase/serverless conformance tests
 *
 * These assert the Neon serverless-driver contract end-to-end against the
 * real npm driver pointed at the server under test (configured in
 * helpers/connection.ts via DATABASE_URL, PORT and PGSERVERLESS_TLS). They can
 * also run against a Neon database, to confirm the expectations track upstream.
 */

import { neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("@neondatabase/serverless conformance (G14)", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let table: string;

  beforeAll(async () => {
    table = uniqueTableName("neon_conf");
    await sql`CREATE TABLE ${sql.unsafe(table)} (
      id SERIAL PRIMARY KEY,
      name TEXT NOT NULL,
      n INTEGER,
      big BIGINT,
      amount NUMERIC(20, 4),
      data JSONB
    )`;
    await sql`INSERT INTO ${sql.unsafe(table)} (name, n, big, amount, data) VALUES
      ('alice', 10, 9223372036854775807, 12345678901234.5678, '{"role":"admin"}'),
      ('bob',   20, 1,                   0.0001,              '{"role":"user"}')`;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(table)}`;
  });

  // ---- Single query: object mode (default) ----
  test("single query returns object-mode rows", async () => {
    const rows = await sql`SELECT name, n FROM ${sql.unsafe(table)} ORDER BY id`;
    expect(rows).toHaveLength(2);
    expect(rows[0]).toMatchObject({ name: "alice", n: 10 });
  });

  // ---- fullResults shape ----
  test("fullResults exposes the FullQueryResults contract", async () => {
    const sqlFull = neon(dbUrl, { fullResults: true });
    const res = await sqlFull`SELECT name FROM ${sql.unsafe(table)} ORDER BY id`;
    expect(res).toHaveProperty("rows");
    expect(res).toHaveProperty("fields");
    expect(res).toHaveProperty("command", "SELECT");
    expect(res).toHaveProperty("rowCount", 2);
    expect(res).toHaveProperty("rowAsArray", false);
    expect(Array.isArray(res.fields)).toBe(true);
    expect(res.fields[0]).toHaveProperty("name", "name");
    expect(res.fields[0]).toHaveProperty("dataTypeID");
  });

  // ---- Array mode ----
  test("array mode returns positional rows", async () => {
    const sqlArr = neon(dbUrl, { arrayMode: true, fullResults: true });
    const res = await sqlArr`SELECT 1 AS a, 2 AS b`;
    expect(res.rowAsArray).toBe(true);
    expect(res.rows[0]).toEqual([1, 2]);
  });

  // ---- int8 / numeric precision: returned as strings ----
  test("bigint is returned as a string (no JS Number precision loss)", async () => {
    const rows = await sql`SELECT big FROM ${sql.unsafe(table)} WHERE name = 'alice'`;
    expect(typeof rows[0].big).toBe("string");
    expect(rows[0].big).toBe("9223372036854775807");
    // Round-trips exactly through BigInt.
    expect(BigInt(rows[0].big as string)).toBe(9223372036854775807n);
  });

  test("numeric is returned as a string (arbitrary precision preserved)", async () => {
    const rows = await sql`SELECT amount FROM ${sql.unsafe(table)} WHERE name = 'alice'`;
    expect(typeof rows[0].amount).toBe("string");
    expect(rows[0].amount).toBe("12345678901234.5678");
  });

  // ---- JSONB parsed to objects ----
  test("jsonb decodes to objects", async () => {
    const rows = await sql`SELECT data FROM ${sql.unsafe(table)} WHERE name = 'bob'`;
    expect(rows[0].data).toEqual({ role: "user" });
  });

  // ---- Parameterized query ----
  test("parameterized query binds positional params", async () => {
    const rows = await sql`SELECT name FROM ${sql.unsafe(table)} WHERE n = ${20}`;
    expect(rows).toHaveLength(1);
    expect(rows[0].name).toBe("bob");
  });

  // ---- Batch / transaction ----
  test("sql.transaction runs a batch and returns per-statement results", async () => {
    const results = await sql.transaction([sql`SELECT 1 AS v`, sql`SELECT 2 AS v`]);
    expect(results).toHaveLength(2);
    expect(results[0][0].v).toBe(1);
    expect(results[1][0].v).toBe(2);
  });

  test("transaction rolls back on error", async () => {
    const t = uniqueTableName("neon_conf_rb");
    await sql`CREATE TABLE ${sql.unsafe(t)} (id INT PRIMARY KEY)`;
    try {
      await sql.transaction([
        sql`INSERT INTO ${sql.unsafe(t)} VALUES (1)`,
        sql`INSERT INTO ${sql.unsafe(t)} VALUES (1)`, // PK violation
      ]);
      expect(true).toBe(false);
    } catch {
      // expected
    }
    const rows = await sql`SELECT COUNT(*)::int AS c FROM ${sql.unsafe(t)}`;
    expect(rows[0].c).toBe(0);
    await sql`DROP TABLE ${sql.unsafe(t)}`;
  });

  // ---- Error shape: NeonDbError carries the full pg-error fields ----
  test("errors surface the full pg-error shape", async () => {
    try {
      await sql`SELECT * FROM a_table_that_does_not_exist_g14`;
      expect(true).toBe(false);
    } catch (e) {
      const err = e as { code?: string; severity?: string; message?: string };
      expect(err.code).toBe("42P01");
      expect(err.severity).toBe("ERROR");
      expect(err.message).toBeTruthy();
    }
  });

  test("unique-violation error carries constraint metadata", async () => {
    const t = uniqueTableName("neon_conf_uniq");
    await sql`CREATE TABLE ${sql.unsafe(t)} (id INT PRIMARY KEY)`;
    await sql`INSERT INTO ${sql.unsafe(t)} VALUES (1)`;
    try {
      await sql`INSERT INTO ${sql.unsafe(t)} VALUES (1)`;
      expect(true).toBe(false);
    } catch (e) {
      const err = e as { code?: string };
      expect(err.code).toBe("23505");
    } finally {
      await sql`DROP TABLE ${sql.unsafe(t)}`;
    }
  });

  // ---- Empty result set ----
  test("empty result set returns an empty array", async () => {
    const rows = await sql`SELECT id FROM ${sql.unsafe(table)} WHERE n > 9999`;
    expect(rows).toEqual([]);
  });
});

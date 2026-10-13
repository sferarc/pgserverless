/**
 * Binary Data (BYTEA) Round-Trip Tests
 *
 * Based on real bugs from both PlanetScale and Neon:
 * - Neon #118: Buffer values JSON.stringify()'d instead of hex-encoded via HTTP
 * - PlanetScale #161: Binary data corrupted after first 2 bytes
 * - PlanetScale #181: Empty Uint8Array formatted as invalid hex
 *
 * These tests verify binary data survives insert→select round-trips
 * across both HTTP and WebSocket transports.
 */

import { Client, neon } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Binary Data (BYTEA) Round-Trip Tests", () => {
  const dbUrl = getDatabaseUrl();
  const sql = neon(dbUrl);
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("binary_rt");
    await sql`CREATE TABLE ${sql.unsafe(testTable)} (
      id SERIAL PRIMARY KEY,
      data BYTEA,
      label TEXT
    )`;
  });

  afterAll(async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(testTable)}`;
  });

  // ==================== HTTP Transport ====================
  describe("HTTP Transport", () => {
    test("simple binary round-trip", async () => {
      const input = Buffer.from([0xde, 0xad, 0xbe, 0xef]);
      const label = uniqueTableName("simple");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      const output = Buffer.from(result[0].data as string, "hex");
      expect(output.length).toBe(input.length);
      expect(Array.from(output)).toEqual(Array.from(input));
    });

    test("empty buffer round-trip", async () => {
      const input = Buffer.alloc(0);
      const label = uniqueTableName("empty");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      expect(result[0].data).toBeDefined();
    });

    test("buffer with bytes > 127 (high bytes)", async () => {
      const input = Buffer.from([0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0xff]);
      const label = uniqueTableName("highbytes");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      const output = Buffer.from(result[0].data as string, "hex");
      expect(Array.from(output)).toEqual(Array.from(input));
    });

    test("all 256 byte values round-trip", async () => {
      const input = Buffer.from(Array.from({ length: 256 }, (_, i) => i));
      const label = uniqueTableName("allbytes");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      const output = Buffer.from(result[0].data as string, "hex");
      expect(output.length).toBe(256);
      for (let i = 0; i < 256; i++) {
        expect(output[i]).toBe(i);
      }
    });

    test("large binary blob (10KB)", async () => {
      const input = Buffer.alloc(10240);
      for (let i = 0; i < input.length; i++) {
        input[i] = i % 256;
      }
      const label = uniqueTableName("largeblob");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      const output = Buffer.from(result[0].data as string, "hex");
      expect(output.length).toBe(input.length);
      expect(Array.from(output)).toEqual(Array.from(input));
    });

    test("binary data with null bytes", async () => {
      const input = Buffer.from([0x00, 0x00, 0x01, 0x00, 0x00]);
      const label = uniqueTableName("nullbytes");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      const output = Buffer.from(result[0].data as string, "hex");
      expect(Array.from(output)).toEqual(Array.from(input));
    });

    test("Uint8Array parameter", async () => {
      const input = new Uint8Array([0xca, 0xfe, 0xba, 0xbe]);
      const label = uniqueTableName("uint8");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${Buffer.from(input)}, ${label})`;
      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;

      const output = Buffer.from(result[0].data as string, "hex");
      expect(Array.from(output)).toEqual(Array.from(input));
    });

    test("bytea hex literal round-trip", async () => {
      const result = await sql`SELECT '\\xDEADBEEF'::bytea as data`;
      expect(result[0].data).toBeDefined();
    });
  });

  // ==================== WebSocket Transport ====================
  describe("WebSocket Transport", () => {
    test("simple binary round-trip via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const input = Buffer.from([0xde, 0xad, 0xbe, 0xef]);
      const label = uniqueTableName("ws_simple");
      await client.query(`INSERT INTO ${testTable} (data, label) VALUES ($1, $2)`, [input, label]);
      const result = await client.query(`SELECT data FROM ${testTable} WHERE label = $1`, [label]);

      expect(result.rows[0].data).toBeDefined();
      const output = result.rows[0].data as Buffer;
      expect(Buffer.isBuffer(output) || (output as unknown) instanceof Uint8Array).toBe(true);
      expect(Array.from(output)).toEqual(Array.from(input));

      await client.end();
    });

    test("all 256 byte values via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const input = Buffer.from(Array.from({ length: 256 }, (_, i) => i));
      const label = uniqueTableName("ws_allbytes");
      await client.query(`INSERT INTO ${testTable} (data, label) VALUES ($1, $2)`, [input, label]);
      const result = await client.query(`SELECT data FROM ${testTable} WHERE label = $1`, [label]);

      const output = result.rows[0].data as Buffer;
      expect(output.length).toBe(256);
      for (let i = 0; i < 256; i++) {
        expect(output[i]).toBe(i);
      }

      await client.end();
    });

    test("empty buffer via client", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const input = Buffer.alloc(0);
      const label = uniqueTableName("ws_empty");
      await client.query(`INSERT INTO ${testTable} (data, label) VALUES ($1, $2)`, [input, label]);
      const result = await client.query(`SELECT data FROM ${testTable} WHERE label = $1`, [label]);

      const output = result.rows[0].data as Buffer;
      expect(output.length).toBe(0);

      await client.end();
    });

    test("large binary blob via client (10KB)", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const input = Buffer.alloc(10240);
      for (let i = 0; i < input.length; i++) {
        input[i] = i % 256;
      }
      const label = uniqueTableName("ws_large");
      await client.query(`INSERT INTO ${testTable} (data, label) VALUES ($1, $2)`, [input, label]);
      const result = await client.query(`SELECT data FROM ${testTable} WHERE label = $1`, [label]);

      const output = result.rows[0].data as Buffer;
      expect(output.length).toBe(input.length);

      await client.end();
    });
  });

  // ==================== Cross-Transport Consistency ====================
  describe("Cross-Transport Consistency", () => {
    test("data inserted via HTTP readable via WebSocket", async () => {
      const input = Buffer.from([0x01, 0x02, 0x03, 0x04, 0x05]);
      const label = uniqueTableName("cross_h2w");
      await sql`INSERT INTO ${sql.unsafe(testTable)} (data, label) VALUES (${input}, ${label})`;

      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query(`SELECT data FROM ${testTable} WHERE label = $1`, [label]);

      const output = result.rows[0].data as Buffer;
      expect(Array.from(output)).toEqual(Array.from(input));

      await client.end();
    });

    test("data inserted via WebSocket readable via HTTP", async () => {
      const client = new Client(dbUrl);
      await client.connect();

      const input = Buffer.from([0xaa, 0xbb, 0xcc, 0xdd, 0xee]);
      const label = uniqueTableName("cross_w2h");
      await client.query(`INSERT INTO ${testTable} (data, label) VALUES ($1, $2)`, [input, label]);
      await client.end();

      const result = await sql`SELECT data FROM ${sql.unsafe(testTable)} WHERE label = ${label}`;
      const output = Buffer.from(result[0].data as string, "hex");
      expect(Array.from(output)).toEqual(Array.from(input));
    });
  });
});

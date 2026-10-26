/**
 * Drizzle ORM over WebSocket and transactions
 *
 * neon-serverless (Pool over GET /v2) and neon-http db.batch
 * (sql.transaction over POST /sql)
 */

import { neon, Pool } from "@neondatabase/serverless";
import { eq } from "drizzle-orm";
import { drizzle as drizzleHttp } from "drizzle-orm/neon-http";
import { drizzle as drizzleWs } from "drizzle-orm/neon-serverless";
import { boolean, integer, jsonb, pgTable, serial, text, timestamp } from "drizzle-orm/pg-core";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

const tableName = uniqueTableName("drizzle_ws");

const items = pgTable(tableName, {
  id: serial("id").primaryKey(),
  name: text("name").notNull().unique(),
  qty: integer("qty"),
  active: boolean("active").default(true),
  meta: jsonb("meta"),
  createdAt: timestamp("created_at", { withTimezone: true }).defaultNow(),
});

describe("Drizzle neon-serverless (WebSocket)", () => {
  const url = getDatabaseUrl();
  const rawSql = neon(url);
  const pool = new Pool({ connectionString: url });
  const db = drizzleWs(pool);

  beforeAll(async () => {
    await rawSql`
      CREATE TABLE ${rawSql.unsafe(tableName)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL UNIQUE,
        qty INTEGER,
        active BOOLEAN DEFAULT true,
        meta JSONB,
        created_at TIMESTAMPTZ DEFAULT NOW()
      )
    `;
  });

  afterAll(async () => {
    await rawSql`DROP TABLE IF EXISTS ${rawSql.unsafe(tableName)}`;
    await pool.end();
  });

  test("insert, select, update and delete round trip", async () => {
    const [inserted] = await db.insert(items).values({ name: "rt", qty: 1 }).returning();
    expect(inserted.name).toBe("rt");

    const selected = await db.select().from(items).where(eq(items.id, inserted.id));
    expect(selected).toHaveLength(1);
    expect(selected[0].qty).toBe(1);

    const [updated] = await db
      .update(items)
      .set({ qty: 2 })
      .where(eq(items.id, inserted.id))
      .returning();
    expect(updated.qty).toBe(2);

    await db.delete(items).where(eq(items.id, inserted.id));
    const remaining = await db.select().from(items).where(eq(items.id, inserted.id));
    expect(remaining).toHaveLength(0);
  });

  test("jsonb, timestamp and boolean types", async () => {
    const meta = { tags: ["a", "b"], nested: { n: 1 } };
    const [row] = await db.insert(items).values({ name: "types", active: false, meta }).returning();
    expect(row.meta).toEqual(meta);
    expect(row.active).toBe(false);
    expect(row.createdAt).toBeInstanceOf(Date);
    expect(Number.isNaN(row.createdAt?.getTime())).toBe(false);
  });

  test("constraint violation surfaces the Postgres error", async () => {
    await db.insert(items).values({ name: "dup" });
    await expect(db.insert(items).values({ name: "dup" })).rejects.toMatchObject({
      cause: { code: "23505" },
    });
  });

  test("transaction commits", async () => {
    await db.transaction(async (tx) => {
      await tx.insert(items).values({ name: "ws_commit_a" });
      await tx.insert(items).values({ name: "ws_commit_b" });
    });
    const rows = await db.select().from(items).where(eq(items.name, "ws_commit_a"));
    expect(rows).toHaveLength(1);
    const rowsB = await db.select().from(items).where(eq(items.name, "ws_commit_b"));
    expect(rowsB).toHaveLength(1);
  });

  test("transaction rolls back when the callback throws", async () => {
    await expect(
      db.transaction(async (tx) => {
        await tx.insert(items).values({ name: "ws_rollback" });
        throw new Error("abort");
      }),
    ).rejects.toThrow("abort");
    const rows = await db.select().from(items).where(eq(items.name, "ws_rollback"));
    expect(rows).toHaveLength(0);
  });

  test("transaction rolls back when a statement fails", async () => {
    await db.insert(items).values({ name: "ws_taken" });
    await expect(
      db.transaction(async (tx) => {
        await tx.insert(items).values({ name: "ws_before_fail" });
        await tx.insert(items).values({ name: "ws_taken" });
      }),
    ).rejects.toMatchObject({ cause: { code: "23505" } });
    const rows = await db.select().from(items).where(eq(items.name, "ws_before_fail"));
    expect(rows).toHaveLength(0);
  });

  test("pool is usable after a failed transaction", async () => {
    await db.insert(items).values({ name: "ws_reuse_taken" });
    await expect(
      db.transaction(async (tx) => {
        await tx.insert(items).values({ name: "ws_reuse_taken" });
      }),
    ).rejects.toMatchObject({ cause: { code: "23505" } });
    await db.transaction(async (tx) => {
      await tx.insert(items).values({ name: "ws_reuse_ok" });
    });
    const rows = await db.select().from(items).where(eq(items.name, "ws_reuse_ok"));
    expect(rows).toHaveLength(1);
  });
});

describe("Drizzle neon-http batch and transaction", () => {
  const rawSql = neon(getDatabaseUrl());
  const db = drizzleHttp(rawSql);
  const httpTable = uniqueTableName("drizzle_batch");
  const batchItems = pgTable(httpTable, {
    id: serial("id").primaryKey(),
    name: text("name").notNull().unique(),
  });

  beforeAll(async () => {
    await rawSql`
      CREATE TABLE ${rawSql.unsafe(httpTable)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL UNIQUE
      )
    `;
  });

  afterAll(async () => {
    await rawSql`DROP TABLE IF EXISTS ${rawSql.unsafe(httpTable)}`;
  });

  test("db.batch runs every statement and returns one result each", async () => {
    const [first, second, selected] = await db.batch([
      db.insert(batchItems).values({ name: "b1" }).returning(),
      db.insert(batchItems).values({ name: "b2" }).returning(),
      db.select().from(batchItems).where(eq(batchItems.name, "b2")),
    ]);
    expect(first[0].name).toBe("b1");
    expect(second[0].name).toBe("b2");
    expect(selected).toHaveLength(1);
  });

  test("db.batch rolls back when one statement fails", async () => {
    await db.insert(batchItems).values({ name: "batch_taken" });
    await expect(
      db.batch([
        db.insert(batchItems).values({ name: "batch_before_fail" }),
        db.insert(batchItems).values({ name: "batch_taken" }),
      ]),
    ).rejects.toMatchObject({ code: "23505" });
    const rows = await db.select().from(batchItems).where(eq(batchItems.name, "batch_before_fail"));
    expect(rows).toHaveLength(0);
  });

  test("db.transaction is rejected by neon-http", async () => {
    await expect(
      db.transaction(async (tx) => {
        await tx.insert(batchItems).values({ name: "http_tx" });
      }),
    ).rejects.toThrow(/No transactions support/);
  });
});

/**
 * Kysely over WebSocket
 *
 * Kysely's built-in PostgresDialect on the driver's pg-compatible Pool
 * (GET /v2), covering what kysely-neon over HTTP cannot: interactive and
 * controlled transactions, savepoints and session state.
 */

import { neon, Pool } from "@neondatabase/serverless";
import type { Generated, PostgresPool } from "kysely";
import { Kysely, PostgresDialect, sql } from "kysely";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

const tableName = uniqueTableName("kysely_ws");

interface ItemTable {
  id: Generated<number>;
  name: string;
  qty: number | null;
  active: Generated<boolean>;
  meta: unknown;
  tags: string[] | null;
  created_at: Generated<Date>;
}

type Database = Record<string, ItemTable>;

describe("Kysely PostgresDialect (WebSocket)", () => {
  const url = getDatabaseUrl();
  const rawSql = neon(url);
  const pool = new Pool({ connectionString: url, max: 4 });
  // The driver's Client.connect resolves to void, not the client; Kysely only uses it to cancel queries
  const db = new Kysely<Database>({
    dialect: new PostgresDialect({ pool: pool as unknown as PostgresPool }),
  });

  const items = () => db.selectFrom(tableName);
  const countByName = async (name: string) => {
    const rows = await items().select("id").where("name", "=", name).execute();
    return rows.length;
  };

  beforeAll(async () => {
    await rawSql`
      CREATE TABLE ${rawSql.unsafe(tableName)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL UNIQUE,
        qty INTEGER,
        active BOOLEAN DEFAULT true,
        meta JSONB,
        tags TEXT[],
        created_at TIMESTAMPTZ DEFAULT NOW()
      )
    `;
  });

  afterAll(async () => {
    await rawSql`DROP TABLE IF EXISTS ${rawSql.unsafe(tableName)}`;
    await db.destroy();
  });

  test("raw sql with bound parameters", async () => {
    const value = "O'Reilly; DROP TABLE x; --";
    const { rows } = await sql<{
      n: number;
      s: string;
    }>`SELECT ${1}::int AS n, ${value}::text AS s`.execute(db);
    expect(rows).toEqual([{ n: 1, s: value }]);
  });

  test("insert, select, update and delete round trip", async () => {
    const inserted = await db
      .insertInto(tableName)
      .values({ name: "rt", qty: 1, meta: null, tags: null })
      .returningAll()
      .executeTakeFirstOrThrow();
    expect(inserted.name).toBe("rt");

    const selected = await items()
      .selectAll()
      .where("id", "=", inserted.id)
      .executeTakeFirstOrThrow();
    expect(selected.qty).toBe(1);

    const updated = await db
      .updateTable(tableName)
      .set({ qty: 2 })
      .where("id", "=", inserted.id)
      .returning("qty")
      .executeTakeFirstOrThrow();
    expect(updated.qty).toBe(2);

    const deleted = await db.deleteFrom(tableName).where("id", "=", inserted.id).executeTakeFirst();
    expect(deleted.numDeletedRows).toBe(1n);
    expect(await countByName("rt")).toBe(0);
  });

  test("jsonb, text[], timestamptz and boolean types", async () => {
    const meta = { nested: { n: 1 }, list: [1, "two", null] };
    const row = await db
      .insertInto(tableName)
      .values({
        name: "types",
        active: false,
        meta: JSON.stringify(meta),
        tags: ["a", "b,c", 'd"e'],
      })
      .returningAll()
      .executeTakeFirstOrThrow();
    expect(row.meta).toEqual(meta);
    expect(row.tags).toEqual(["a", "b,c", 'd"e']);
    expect(row.active).toBe(false);
    expect(row.created_at).toBeInstanceOf(Date);
    expect(Number.isNaN(row.created_at.getTime())).toBe(false);
  });

  test("multi-row insert reports every inserted row", async () => {
    const rows = await db
      .insertInto(tableName)
      .values([
        { name: "multi_a", qty: 1, meta: null, tags: null },
        { name: "multi_b", qty: 2, meta: null, tags: null },
        { name: "multi_c", qty: 3, meta: null, tags: null },
      ])
      .returning("qty")
      .execute();
    expect(rows.map((r) => r.qty)).toEqual([1, 2, 3]);
  });

  test("constraint violation surfaces the Postgres error", async () => {
    await db
      .insertInto(tableName)
      .values({ name: "dup", qty: null, meta: null, tags: null })
      .execute();
    await expect(
      db.insertInto(tableName).values({ name: "dup", qty: null, meta: null, tags: null }).execute(),
    ).rejects.toMatchObject({ code: "23505" });
  });

  test("transaction commits", async () => {
    await db.transaction().execute(async (trx) => {
      await trx
        .insertInto(tableName)
        .values({ name: "tx_commit_a", qty: null, meta: null, tags: null })
        .execute();
      await trx
        .insertInto(tableName)
        .values({ name: "tx_commit_b", qty: null, meta: null, tags: null })
        .execute();
    });
    expect(await countByName("tx_commit_a")).toBe(1);
    expect(await countByName("tx_commit_b")).toBe(1);
  });

  test("transaction rolls back when the callback throws", async () => {
    await expect(
      db.transaction().execute(async (trx) => {
        await trx
          .insertInto(tableName)
          .values({ name: "tx_throw", qty: null, meta: null, tags: null })
          .execute();
        throw new Error("abort");
      }),
    ).rejects.toThrow("abort");
    expect(await countByName("tx_throw")).toBe(0);
  });

  test("transaction rolls back when a statement fails", async () => {
    await db
      .insertInto(tableName)
      .values({ name: "tx_taken", qty: null, meta: null, tags: null })
      .execute();
    await expect(
      db.transaction().execute(async (trx) => {
        await trx
          .insertInto(tableName)
          .values({ name: "tx_before_fail", qty: null, meta: null, tags: null })
          .execute();
        await trx
          .insertInto(tableName)
          .values({ name: "tx_taken", qty: null, meta: null, tags: null })
          .execute();
      }),
    ).rejects.toMatchObject({ code: "23505" });
    expect(await countByName("tx_before_fail")).toBe(0);
  });

  test("isolation level and access mode reach the server", async () => {
    const settings = await db
      .transaction()
      .setIsolationLevel("serializable")
      .setAccessMode("read only")
      .execute(async (trx) => {
        const { rows } = await sql<{ iso: string; ro: string }>`
          SELECT current_setting('transaction_isolation') AS iso,
                 current_setting('transaction_read_only') AS ro
        `.execute(trx);
        return rows[0];
      });
    expect(settings).toEqual({ iso: "serializable", ro: "on" });
  });

  test("read-only transaction rejects writes", async () => {
    await expect(
      db
        .transaction()
        .setAccessMode("read only")
        .execute(async (trx) => {
          await trx
            .insertInto(tableName)
            .values({ name: "tx_ro", qty: null, meta: null, tags: null })
            .execute();
        }),
    ).rejects.toMatchObject({ code: "25006" });
    expect(await countByName("tx_ro")).toBe(0);
  });

  test("controlled transaction with savepoint rollback and release", async () => {
    const trx = await db.startTransaction().execute();
    try {
      await trx
        .insertInto(tableName)
        .values({ name: "sp_kept", qty: null, meta: null, tags: null })
        .execute();
      const afterKept = await trx.savepoint("after_kept").execute();
      await afterKept
        .insertInto(tableName)
        .values({ name: "sp_undone", qty: null, meta: null, tags: null })
        .execute();
      await afterKept.rollbackToSavepoint("after_kept").execute();

      const afterUndo = await afterKept.savepoint("after_undo").execute();
      await afterUndo
        .insertInto(tableName)
        .values({ name: "sp_released", qty: null, meta: null, tags: null })
        .execute();
      await afterUndo.releaseSavepoint("after_undo").execute();
      await trx.commit().execute();
    } catch (err) {
      await trx.rollback().execute();
      throw err;
    }
    expect(await countByName("sp_kept")).toBe(1);
    expect(await countByName("sp_undone")).toBe(0);
    expect(await countByName("sp_released")).toBe(1);
  });

  test("savepoint recovers a transaction after a failed statement", async () => {
    await db
      .insertInto(tableName)
      .values({ name: "sp_taken", qty: null, meta: null, tags: null })
      .execute();
    const trx = await db.startTransaction().execute();
    try {
      const sp = await trx.savepoint("before_dup").execute();
      await expect(
        sp
          .insertInto(tableName)
          .values({ name: "sp_taken", qty: null, meta: null, tags: null })
          .execute(),
      ).rejects.toMatchObject({ code: "23505" });
      await sp.rollbackToSavepoint("before_dup").execute();
      await trx
        .insertInto(tableName)
        .values({ name: "sp_after_dup", qty: null, meta: null, tags: null })
        .execute();
      await trx.commit().execute();
    } catch (err) {
      await trx.rollback().execute();
      throw err;
    }
    expect(await countByName("sp_after_dup")).toBe(1);
  });

  test("controlled transaction rollback discards its writes", async () => {
    const trx = await db.startTransaction().execute();
    await trx
      .insertInto(tableName)
      .values({ name: "ctl_rollback", qty: null, meta: null, tags: null })
      .execute();
    await trx.rollback().execute();
    expect(trx.isRolledBack).toBe(true);
    expect(await countByName("ctl_rollback")).toBe(0);
  });

  test("pool is usable after a failed transaction", async () => {
    await db
      .insertInto(tableName)
      .values({ name: "reuse_taken", qty: null, meta: null, tags: null })
      .execute();
    await expect(
      db.transaction().execute(async (trx) => {
        await trx
          .insertInto(tableName)
          .values({ name: "reuse_taken", qty: null, meta: null, tags: null })
          .execute();
      }),
    ).rejects.toMatchObject({ code: "23505" });
    await db.transaction().execute(async (trx) => {
      await trx
        .insertInto(tableName)
        .values({ name: "reuse_ok", qty: null, meta: null, tags: null })
        .execute();
    });
    expect(await countByName("reuse_ok")).toBe(1);
  });

  test("session state persists on a reserved connection", async () => {
    const zone = await db.connection().execute(async (conn) => {
      await sql`SET TIME ZONE 'Asia/Kolkata'`.execute(conn);
      const { rows } = await sql<{ tz: string }>`SELECT current_setting('TimeZone') AS tz`.execute(
        conn,
      );
      await sql`RESET TIME ZONE`.execute(conn);
      return rows[0].tz;
    });
    expect(zone).toBe("Asia/Kolkata");
  });

  test("concurrent queries share the pool", async () => {
    const results = await Promise.all(
      Array.from({ length: 8 }, (_, i) =>
        sql<{ n: number }>`SELECT ${i}::int AS n, pg_sleep(0.05)`.execute(db),
      ),
    );
    expect(results.map((r) => r.rows[0].n)).toEqual([0, 1, 2, 3, 4, 5, 6, 7]);
  });
});

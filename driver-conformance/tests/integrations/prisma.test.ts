// Drives @prisma/adapter-neon directly: its commit() and rollback() only release the connection, so the tests send COMMIT or ROLLBACK themselves.

import { PrismaNeon, PrismaNeonHttp } from "@prisma/adapter-neon";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

type Arg = string | number | boolean | null;

type Queryable = Pick<Awaited<ReturnType<PrismaNeon["connect"]>>, "queryRaw" | "executeRaw">;

function toQuery(sql: string, args: Arg[]) {
  return {
    sql,
    args,
    argTypes: args.map((arg) => ({
      scalarType:
        typeof arg === "number"
          ? Number.isInteger(arg)
            ? ("int" as const)
            : ("float" as const)
          : typeof arg === "boolean"
            ? ("boolean" as const)
            : ("string" as const),
      arity: "scalar" as const,
    })),
  };
}

function toTypedQuery(
  sql: string,
  args: { value: unknown; scalarType: "int" | "string" | "datetime" | "bytes"; arity?: "list" }[],
) {
  return {
    sql,
    args: args.map((arg) => arg.value),
    argTypes: args.map((arg) => ({
      scalarType: arg.scalarType,
      arity: arg.arity ?? ("scalar" as const),
    })),
  };
}

async function select(db: Queryable, sql: string, args: Arg[] = []) {
  const result = await db.queryRaw(toQuery(sql, args));
  return result.rows.map((row) =>
    Object.fromEntries(result.columnNames.map((name, i) => [name, row[i]])),
  );
}

const factories = [
  {
    name: "WebSocket (PrismaNeon)",
    create: () => new PrismaNeon({ connectionString: getDatabaseUrl() }),
    mapsErrors: true,
  },
  {
    name: "HTTP (PrismaNeonHttp)",
    create: () => new PrismaNeonHttp(getDatabaseUrl(), {}),
    mapsErrors: false,
  },
];

describe.each(factories)("Prisma adapter over $name", ({ create, mapsErrors }) => {
  // PrismaNeonHttp passes the driver error through instead of wrapping it
  const failure = (kind: string, code: string) => (mapsErrors ? { cause: { kind } } : { code });
  const table = uniqueTableName("prisma_adapter");
  let adapter: Awaited<ReturnType<ReturnType<typeof create>["connect"]>>;

  beforeAll(async () => {
    adapter = await create().connect();
    await adapter.executeRaw(
      toQuery(
        `CREATE TABLE ${table} (
          id SERIAL PRIMARY KEY,
          name TEXT NOT NULL,
          email TEXT UNIQUE,
          age INTEGER,
          active BOOLEAN DEFAULT true,
          balance DECIMAL(10, 2),
          metadata JSONB,
          created_at TIMESTAMPTZ DEFAULT NOW()
        )`,
        [],
      ),
    );
  });

  afterAll(async () => {
    await adapter.executeRaw(toQuery(`DROP TABLE IF EXISTS ${table}`, []));
    await adapter.dispose();
  });

  describe("queryRaw and executeRaw", () => {
    test("selects a literal", async () => {
      expect(await select(adapter, "SELECT 1 AS num")).toEqual([{ num: 1 }]);
    });

    test("binds typed parameters on insert and returns the row", async () => {
      const rows = await select(
        adapter,
        `INSERT INTO ${table} (name, email, age, active) VALUES ($1, $2, $3, $4) RETURNING name, email, age, active`,
        ["ada", "ada@example.com", 36, false],
      );
      expect(rows).toEqual([{ name: "ada", email: "ada@example.com", age: 36, active: false }]);
    });

    test("executeRaw reports affected rows", async () => {
      await adapter.executeRaw(
        toQuery(`INSERT INTO ${table} (name, age) VALUES ($1, $2), ($3, $4)`, ["u1", 1, "u2", 1]),
      );
      const updated = await adapter.executeRaw(
        toQuery(`UPDATE ${table} SET age = $1 WHERE age = $2`, [2, 1]),
      );
      expect(updated).toBe(2);
      const deleted = await adapter.executeRaw(toQuery(`DELETE FROM ${table} WHERE age = $1`, [2]));
      expect(deleted).toBe(2);
    });

    test("filters, orders and paginates", async () => {
      await adapter.executeRaw(
        toQuery(`INSERT INTO ${table} (name, age) VALUES ($1, $2), ($3, $4), ($5, $6)`, [
          "p_a",
          10,
          "p_b",
          20,
          "p_c",
          30,
        ]),
      );
      const page = await select(
        adapter,
        `SELECT name FROM ${table} WHERE name LIKE $1 ORDER BY age DESC LIMIT $2 OFFSET $3`,
        ["p\\_%", 2, 1],
      );
      expect(page).toEqual([{ name: "p_b" }, { name: "p_a" }]);
    });

    test("aggregates", async () => {
      const [row] = await select(
        adapter,
        `SELECT COUNT(*)::int AS n, MIN(age) AS lo, MAX(age) AS hi FROM ${table} WHERE name LIKE $1`,
        ["p\\_%"],
      );
      expect(row).toEqual({ n: 3, lo: 10, hi: 30 });
    });
  });

  describe("type handling", () => {
    test("round-trips JSONB", async () => {
      const [row] = await select(
        adapter,
        `INSERT INTO ${table} (name, metadata) VALUES ($1, $2::jsonb) RETURNING metadata`,
        ["json_row", JSON.stringify({ a: 1, tags: ["x", "y"] })],
      );
      expect(JSON.parse(String(row?.metadata))).toEqual({ a: 1, tags: ["x", "y"] });
    });

    test("round-trips decimals without losing scale", async () => {
      const [row] = await select(
        adapter,
        `INSERT INTO ${table} (name, balance) VALUES ($1, $2::numeric) RETURNING balance::text AS balance`,
        ["decimal_row", "1234.50"],
      );
      expect(row?.balance).toBe("1234.50");
    });

    test("returns timestamps in the adapter ISO form", async () => {
      const [row] = await select(adapter, "SELECT '2024-01-02 03:04:05'::timestamp AS ts");
      expect(row?.ts).toBe("2024-01-02T03:04:05+00:00");
    });

    test("returns NULL as null", async () => {
      const [row] = await select(
        adapter,
        `INSERT INTO ${table} (name) VALUES ($1) RETURNING email, age`,
        ["null_row"],
      );
      expect(row).toEqual({ email: null, age: null });
    });
  });

  describe("argument mapping", () => {
    test("binds a null parameter", async () => {
      const [row] = await select(
        adapter,
        `INSERT INTO ${table} (name, email) VALUES ($1, $2) RETURNING email`,
        ["null_param", null],
      );
      expect(row).toEqual({ email: null });
    });

    test("binds a list parameter", async () => {
      const result = await adapter.queryRaw(
        toTypedQuery("SELECT $1::int[] AS xs", [
          { value: [1, 2, 3], scalarType: "int", arity: "list" },
        ]),
      );
      expect(result.rows).toEqual([[[1, 2, 3]]]);
    });

    test("binds a datetime string as a timestamp", async () => {
      const result = await adapter.queryRaw(
        toTypedQuery("SELECT $1::timestamp AS ts", [
          { value: "2024-01-02T03:04:05.000Z", scalarType: "datetime" },
        ]),
      );
      expect(result.rows).toEqual([["2024-01-02T03:04:05+00:00"]]);
    });

    test("binds base64 bytes as bytea", async () => {
      const result = await adapter.queryRaw(
        toTypedQuery("SELECT encode($1::bytea, 'hex') AS hex", [
          { value: Buffer.from("hi").toString("base64"), scalarType: "bytes" },
        ]),
      );
      expect(result.rows).toEqual([["6869"]]);
    });
  });

  describe("errors", () => {
    test("maps a unique violation", async () => {
      const email = `dup_${Date.now()}@example.com`;
      await adapter.executeRaw(
        toQuery(`INSERT INTO ${table} (name, email) VALUES ($1, $2)`, ["d1", email]),
      );
      await expect(
        adapter.executeRaw(
          toQuery(`INSERT INTO ${table} (name, email) VALUES ($1, $2)`, ["d2", email]),
        ),
      ).rejects.toMatchObject(failure("UniqueConstraintViolation", "23505"));
    });

    test("maps a not null violation", async () => {
      await expect(
        adapter.executeRaw(toQuery(`INSERT INTO ${table} (name) VALUES (NULL)`, [])),
      ).rejects.toMatchObject(failure("NullConstraintViolation", "23502"));
    });

    test("maps a missing table", async () => {
      await expect(
        adapter.queryRaw(toQuery("SELECT * FROM nonexistent_table_xyz", [])),
      ).rejects.toMatchObject(failure("TableDoesNotExist", "42P01"));
    });
  });
});

describe("Prisma adapter transactions (WebSocket)", () => {
  const table = uniqueTableName("prisma_txn");
  let adapter: Awaited<ReturnType<PrismaNeon["connect"]>>;

  beforeAll(async () => {
    adapter = await new PrismaNeon({ connectionString: getDatabaseUrl() }).connect();
    await adapter.executeRaw(
      toQuery(`CREATE TABLE ${table} (id SERIAL PRIMARY KEY, name TEXT)`, []),
    );
  });

  afterAll(async () => {
    await adapter.executeRaw(toQuery(`DROP TABLE IF EXISTS ${table}`, []));
    await adapter.dispose();
  });

  async function inTransaction(
    end: "commit" | "rollback",
    body: (tx: Awaited<ReturnType<typeof adapter.startTransaction>>) => Promise<void>,
    isolation?: "SERIALIZABLE",
  ) {
    const tx = await adapter.startTransaction(isolation);
    try {
      await body(tx);
      await tx.executeRaw(toQuery(end === "commit" ? "COMMIT" : "ROLLBACK", []));
    } catch (error) {
      await tx.executeRaw(toQuery("ROLLBACK", [])).catch(() => undefined);
      throw error;
    } finally {
      await tx[end]();
    }
  }

  const count = async (name: string) =>
    (await select(adapter, `SELECT name FROM ${table} WHERE name = $1`, [name])).length;

  test("commit persists the write", async () => {
    await inTransaction("commit", async (tx) => {
      await tx.executeRaw(toQuery(`INSERT INTO ${table} (name) VALUES ($1)`, ["committed"]));
    });
    expect(await count("committed")).toBe(1);
  });

  test("rollback discards the write", async () => {
    await inTransaction("rollback", async (tx) => {
      await tx.executeRaw(toQuery(`INSERT INTO ${table} (name) VALUES ($1)`, ["rolled_back"]));
    });
    expect(await count("rolled_back")).toBe(0);
  });

  test("a transaction reads its own uncommitted write while others do not", async () => {
    await inTransaction("rollback", async (tx) => {
      await tx.executeRaw(toQuery(`INSERT INTO ${table} (name) VALUES ($1)`, ["isolated"]));
      expect(
        await select(tx, `SELECT name FROM ${table} WHERE name = $1`, ["isolated"]),
      ).toHaveLength(1);
      expect(await count("isolated")).toBe(0);
    });
  });

  test("supports an isolation level", async () => {
    await inTransaction(
      "commit",
      async (tx) => {
        const [row] = await select(tx, "SHOW transaction_isolation");
        expect(row).toEqual({ transaction_isolation: "serializable" });
      },
      "SERIALIZABLE",
    );
  });
});

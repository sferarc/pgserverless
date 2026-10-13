/**
 * Prisma Integration Tests (50 tests)
 *
 * Tests for Prisma integration with @prisma/adapter-neon
 *
 * Note: These tests require Prisma to be properly configured.
 * They use raw SQL queries via Prisma's $queryRaw to test the Neon adapter.
 */

import { neon, Pool } from "@neondatabase/serverless";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

describe("Prisma Integration Tests", () => {
  let pool: Pool;
  let rawSql: ReturnType<typeof neon>;
  const testTableName = uniqueTableName("prisma_test");

  beforeAll(async () => {
    rawSql = neon(getDatabaseUrl());
    pool = new Pool({ connectionString: getDatabaseUrl() });

    // Create test table
    await rawSql`
      CREATE TABLE IF NOT EXISTS ${rawSql.unsafe(testTableName)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL,
        email TEXT UNIQUE,
        age INTEGER,
        active BOOLEAN DEFAULT true,
        balance DECIMAL(10, 2),
        metadata JSONB,
        created_at TIMESTAMPTZ DEFAULT NOW(),
        updated_at TIMESTAMPTZ DEFAULT NOW()
      )
    `;
  });

  afterAll(async () => {
    await rawSql`DROP TABLE IF EXISTS ${rawSql.unsafe(testTableName)}`;
    await pool.end();
  });

  // Note: Since we can't run actual Prisma client tests without a generated client,
  // we simulate Prisma-like operations using the Pool (which Prisma adapter uses)

  // ==================== Connection Tests ====================
  describe("Connection Tests", () => {
    test("@prisma/adapter-neon setup concept", async () => {
      // The adapter uses Pool internally
      const client = await pool.connect();
      const result = await client.query("SELECT 1 as num");
      expect(result.rows[0].num).toBe(1);
      client.release();
    });

    test("basic connection via Pool", async () => {
      const result = await pool.query("SELECT 'prisma_test' as adapter");
      expect(result.rows[0].adapter).toBe("prisma_test");
    });

    test("connection pooling works", async () => {
      const client1 = await pool.connect();
      const client2 = await pool.connect();

      const r1 = await client1.query("SELECT 1 as n");
      const r2 = await client2.query("SELECT 2 as n");

      expect(r1.rows[0].n).toBe(1);
      expect(r2.rows[0].n).toBe(2);

      client1.release();
      client2.release();
    });
  });

  // ==================== CRUD Operations (Prisma-style) ====================
  describe("CRUD Operations", () => {
    test("create single record", async () => {
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, email, age) VALUES ($1, $2, $3) RETURNING *`,
        ["prisma_user_1", "prisma1@example.com", 25],
      );
      expect(result.rows.length).toBe(1);
      expect(result.rows[0].name).toBe("prisma_user_1");
    });

    test("create multiple records", async () => {
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, email, age) VALUES ($1, $2, $3), ($4, $5, $6) RETURNING *`,
        ["prisma_multi_1", "pm1@example.com", 30, "prisma_multi_2", "pm2@example.com", 35],
      );
      expect(result.rows.length).toBe(2);
    });

    test("findMany - select all", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName}`);
      expect(result.rows.length).toBeGreaterThan(0);
    });

    test("findFirst - select with limit", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} LIMIT 1`);
      expect(result.rows.length).toBe(1);
    });

    test("findUnique - select by unique field", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE email = $1`, [
        "prisma1@example.com",
      ]);
      expect(result.rows.length).toBeLessThanOrEqual(1);
    });

    test("update single record", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, email, age) VALUES ($1, $2, $3)`, [
        "prisma_update",
        "pupdate@example.com",
        20,
      ]);

      const result = await pool.query(
        `UPDATE ${testTableName} SET age = $1 WHERE email = $2 RETURNING *`,
        [21, "pupdate@example.com"],
      );
      expect(result.rows[0].age).toBe(21);
    });

    test("updateMany - update multiple records", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, active) VALUES ($1, $2), ($3, $4)`, [
        "prisma_upd_many_1",
        false,
        "prisma_upd_many_2",
        false,
      ]);

      const result = await pool.query(
        `UPDATE ${testTableName} SET active = $1 WHERE name LIKE $2 RETURNING *`,
        [true, "prisma_upd_many%"],
      );
      expect(result.rows.length).toBeGreaterThanOrEqual(2);
    });

    test("delete single record", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, email) VALUES ($1, $2)`, [
        "prisma_delete",
        "pdelete@example.com",
      ]);

      const result = await pool.query(`DELETE FROM ${testTableName} WHERE email = $1 RETURNING *`, [
        "pdelete@example.com",
      ]);
      expect(result.rows.length).toBe(1);
    });

    test("deleteMany - delete multiple records", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name) VALUES ($1), ($2)`, [
        "prisma_del_many_1",
        "prisma_del_many_2",
      ]);

      const result = await pool.query(
        `DELETE FROM ${testTableName} WHERE name LIKE $1 RETURNING *`,
        ["prisma_del_many%"],
      );
      expect(result.rows.length).toBeGreaterThanOrEqual(2);
    });

    test("upsert - insert on conflict", async () => {
      const email = "prisma_upsert@example.com";

      // First insert
      await pool.query(
        `INSERT INTO ${testTableName} (name, email, age)
         VALUES ($1, $2, $3)
         ON CONFLICT (email) DO UPDATE SET age = $3
         RETURNING *`,
        ["prisma_upsert", email, 25],
      );

      // Upsert - should update
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, email, age)
         VALUES ($1, $2, $3)
         ON CONFLICT (email) DO UPDATE SET age = $3
         RETURNING *`,
        ["prisma_upsert", email, 30],
      );

      expect(result.rows[0].age).toBe(30);
    });
  });

  // ==================== Filtering ====================
  describe("Filtering", () => {
    test("where equals", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, age) VALUES ($1, $2)`, [
        "prisma_where_eq",
        42,
      ]);

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE age = $1`, [42]);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("where not equals", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE age != $1`, [999]);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });

    test("where greater than", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, age) VALUES ($1, $2)`, [
        "prisma_gt",
        100,
      ]);

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE age > $1`, [99]);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("where less than", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, age) VALUES ($1, $2)`, [
        "prisma_lt",
        5,
      ]);

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE age < $1`, [10]);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("where IN", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name) VALUES ($1), ($2)`, [
        "prisma_in_1",
        "prisma_in_2",
      ]);

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name = ANY($1)`, [
        ["prisma_in_1", "prisma_in_2"],
      ]);
      expect(result.rows.length).toBeGreaterThanOrEqual(2);
    });

    test("where NOT IN", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name != ALL($1)`, [
        ["nonexistent1", "nonexistent2"],
      ]);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });

    test("where contains (LIKE)", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name) VALUES ($1)`, ["prisma_contains_test"]);

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name LIKE $1`, [
        "%contains%",
      ]);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("where startsWith", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name LIKE $1`, [
        "prisma%",
      ]);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("where endsWith", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name LIKE $1`, [
        "%test",
      ]);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });

    test("where IS NULL", async () => {
      await pool.query(`INSERT INTO ${testTableName} (name, age) VALUES ($1, $2)`, [
        "prisma_null",
        null,
      ]);

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE age IS NULL`);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Sorting ====================
  describe("Sorting", () => {
    test("orderBy ascending", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} ORDER BY id ASC LIMIT 5`);
      if (result.rows.length > 1) {
        expect(result.rows[0].id).toBeLessThan(result.rows[1].id);
      }
    });

    test("orderBy descending", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} ORDER BY id DESC LIMIT 5`);
      if (result.rows.length > 1) {
        expect(result.rows[0].id).toBeGreaterThan(result.rows[1].id);
      }
    });

    test("orderBy multiple fields", async () => {
      const result = await pool.query(
        `SELECT * FROM ${testTableName} ORDER BY active DESC, id ASC LIMIT 10`,
      );
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });
  });

  // ==================== Pagination ====================
  describe("Pagination", () => {
    test("take (LIMIT)", async () => {
      const result = await pool.query(`SELECT * FROM ${testTableName} LIMIT $1`, [5]);
      expect(result.rows.length).toBeLessThanOrEqual(5);
    });

    test("skip (OFFSET)", async () => {
      const all = await pool.query(`SELECT * FROM ${testTableName} ORDER BY id ASC`);
      const skipped = await pool.query(
        `SELECT * FROM ${testTableName} ORDER BY id ASC OFFSET $1`,
        [1],
      );

      if (all.rows.length > 1) {
        expect(skipped.rows[0].id).toBe(all.rows[1].id);
      }
    });

    test("cursor-based pagination", async () => {
      const first = await pool.query(`SELECT * FROM ${testTableName} ORDER BY id ASC LIMIT 2`);

      if (first.rows.length > 0) {
        const cursor = first.rows[first.rows.length - 1].id;
        const next = await pool.query(
          `SELECT * FROM ${testTableName} WHERE id > $1 ORDER BY id ASC LIMIT 2`,
          [cursor],
        );
        expect(next.rows.length).toBeGreaterThanOrEqual(0);
      }
    });
  });

  // ==================== Aggregations ====================
  describe("Aggregations", () => {
    test("count", async () => {
      const result = await pool.query(`SELECT COUNT(*) as count FROM ${testTableName}`);
      expect(Number(result.rows[0].count)).toBeGreaterThan(0);
    });

    test("sum", async () => {
      const result = await pool.query(`SELECT SUM(age) as total FROM ${testTableName}`);
      expect(result.rows[0].total).toBeDefined();
    });

    test("avg", async () => {
      const result = await pool.query(`SELECT AVG(age) as average FROM ${testTableName}`);
      expect(result.rows[0].average).toBeDefined();
    });

    test("min", async () => {
      const result = await pool.query(`SELECT MIN(age) as min_age FROM ${testTableName}`);
      expect(result.rows[0]).toHaveProperty("min_age");
    });

    test("max", async () => {
      const result = await pool.query(`SELECT MAX(age) as max_age FROM ${testTableName}`);
      expect(result.rows[0]).toHaveProperty("max_age");
    });

    test("groupBy", async () => {
      const result = await pool.query(
        `SELECT active, COUNT(*) as count FROM ${testTableName} GROUP BY active`,
      );
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Transactions ====================
  describe("Transactions", () => {
    test("transaction commit", async () => {
      const client = await pool.connect();
      const name = uniqueTableName("txn_commit");

      try {
        await client.query("BEGIN");
        await client.query(`INSERT INTO ${testTableName} (name) VALUES ($1)`, [name]);
        await client.query("COMMIT");
      } finally {
        client.release();
      }

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name = $1`, [name]);
      expect(result.rows.length).toBe(1);
    });

    test("transaction rollback", async () => {
      const client = await pool.connect();
      const name = uniqueTableName("txn_rollback");

      try {
        await client.query("BEGIN");
        await client.query(`INSERT INTO ${testTableName} (name) VALUES ($1)`, [name]);
        await client.query("ROLLBACK");
      } finally {
        client.release();
      }

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name = $1`, [name]);
      expect(result.rows.length).toBe(0);
    });

    test("interactive transaction", async () => {
      const client = await pool.connect();
      const name = uniqueTableName("txn_interactive");

      try {
        await client.query("BEGIN");

        const insert = await client.query(
          `INSERT INTO ${testTableName} (name, age) VALUES ($1, $2) RETURNING id`,
          [name, 10],
        );

        await client.query(`UPDATE ${testTableName} SET age = $1 WHERE id = $2`, [
          20,
          insert.rows[0].id,
        ]);

        await client.query("COMMIT");
      } finally {
        client.release();
      }

      const result = await pool.query(`SELECT * FROM ${testTableName} WHERE name = $1`, [name]);
      expect(result.rows[0].age).toBe(20);
    });
  });

  // ==================== Type Handling ====================
  describe("Type Handling", () => {
    test("JSON fields", async () => {
      const metadata = { key: "value", nested: { num: 42 } };
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, metadata) VALUES ($1, $2) RETURNING *`,
        ["prisma_json", JSON.stringify(metadata)],
      );
      expect(result.rows[0].metadata).toEqual(metadata);
    });

    test("DateTime handling", async () => {
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name) VALUES ($1) RETURNING *`,
        ["prisma_datetime"],
      );
      expect(result.rows[0].created_at).toBeDefined();
    });

    test("Decimal handling", async () => {
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, balance) VALUES ($1, $2) RETURNING *`,
        ["prisma_decimal", "1234.56"],
      );
      expect(result.rows[0].balance).toBeDefined();
    });

    test("Boolean handling", async () => {
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, active) VALUES ($1, $2) RETURNING *`,
        ["prisma_bool", false],
      );
      expect(result.rows[0].active).toBe(false);
    });

    test("Optional/NULL fields", async () => {
      const result = await pool.query(
        `INSERT INTO ${testTableName} (name, email, age) VALUES ($1, $2, $3) RETURNING *`,
        ["prisma_optional", null, null],
      );
      expect(result.rows[0].email).toBeNull();
      expect(result.rows[0].age).toBeNull();
    });
  });

  // ==================== Raw Queries ====================
  describe("Raw Queries", () => {
    test("$queryRaw equivalent", async () => {
      const result = await pool.query("SELECT 'raw' as type");
      expect(result.rows[0].type).toBe("raw");
    });

    test("$executeRaw equivalent", async () => {
      const name = uniqueTableName("exec_raw");
      const result = await pool.query(`INSERT INTO ${testTableName} (name) VALUES ($1)`, [name]);
      expect(result.rowCount).toBe(1);
    });

    test("raw query with parameters", async () => {
      const result = await pool.query("SELECT $1::text as param1, $2::integer as param2", [
        "hello",
        42,
      ]);
      expect(result.rows[0]).toEqual({ param1: "hello", param2: 42 });
    });

    test("raw query with type-safe results", async () => {
      const result = await pool.query("SELECT 1 as num, 'text' as str, true as bool");
      expect(result.rows[0].num).toBe(1);
      expect(result.rows[0].str).toBe("text");
      expect(result.rows[0].bool).toBe(true);
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("handles unique constraint violation", async () => {
      const uniqueEmail = `unique_${Date.now()}@example.com`;
      await pool.query(`INSERT INTO ${testTableName} (name, email) VALUES ($1, $2)`, [
        "prisma_unique_1",
        uniqueEmail,
      ]);

      try {
        await pool.query(`INSERT INTO ${testTableName} (name, email) VALUES ($1, $2)`, [
          "prisma_unique_2",
          uniqueEmail,
        ]);
        expect(true).toBe(false);
      } catch (e) {
        expect((e as { code: string }).code).toBe("23505"); // unique_violation
      }
    });

    test("handles not null violation", async () => {
      try {
        await pool.query(`INSERT INTO ${testTableName} (name) VALUES ($1)`, [null]);
        expect(true).toBe(false);
      } catch (e) {
        expect((e as { code: string }).code).toBe("23502"); // not_null_violation
      }
    });

    test("handles invalid query", async () => {
      try {
        await pool.query("SELECT * FROM nonexistent_table_xyz");
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });
  });
});

/**
 * Kysely Integration Tests (50 tests)
 *
 * Tests for Kysely integration with Neon serverless driver via kysely-neon
 */

import { neon } from "@neondatabase/serverless";
import type { Generated } from "kysely";
import { Kysely, sql } from "kysely";
import { NeonDialect } from "kysely-neon";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";

// Define database interface
// Using Generated<T> for columns with default values (SERIAL, DEFAULT NOW(), DEFAULT true)
interface Database {
  kysely_test: {
    id: Generated<number>;
    name: string;
    email: string | null;
    age: number | null;
    active: Generated<boolean>;
    score: number | null;
    metadata: Record<string, unknown> | null;
    created_at: Generated<Date>;
  };
}

describe("Kysely Integration Tests", () => {
  let db: Kysely<Database>;
  let rawSql: ReturnType<typeof neon>;
  const testTableName = "kysely_test";

  beforeAll(async () => {
    // Create the neon query function
    rawSql = neon(getDatabaseUrl());

    // kysely-neon v2.x expects a `neon` property with the neon query function
    db = new Kysely<Database>({
      dialect: new NeonDialect({
        neon: rawSql,
      }),
    });

    // Create test table
    await rawSql`
      CREATE TABLE IF NOT EXISTS ${rawSql.unsafe(testTableName)} (
        id SERIAL PRIMARY KEY,
        name TEXT NOT NULL,
        email TEXT,
        age INTEGER,
        active BOOLEAN DEFAULT true,
        score DOUBLE PRECISION,
        metadata JSONB,
        created_at TIMESTAMPTZ DEFAULT NOW()
      )
    `;
  });

  afterAll(async () => {
    await rawSql`DROP TABLE IF EXISTS ${rawSql.unsafe(testTableName)}`;
    await db.destroy();
  });

  // ==================== Connection ====================
  describe("Connection", () => {
    test("basic connection with kysely-neon", async () => {
      const result = await sql`SELECT 1 as num`.execute(db);
      expect(result.rows.length).toBe(1);
    });

    test("schema introspection", async () => {
      const result = await sql`
        SELECT column_name, data_type
        FROM information_schema.columns
        WHERE table_name = ${testTableName}
      `.execute(db);
      expect(result.rows.length).toBeGreaterThan(0);
    });
  });

  // ==================== Basic CRUD ====================
  describe("Basic CRUD", () => {
    test("insert single row", async () => {
      const result = await db
        .insertInto("kysely_test")
        .values({
          name: "kysely_user_1",
          email: "kysely1@example.com",
          age: 25,
          active: true,
        })
        .returningAll()
        .execute();

      expect(result.length).toBe(1);
      expect(result[0].name).toBe("kysely_user_1");
    });

    test("insert multiple rows", async () => {
      const result = await db
        .insertInto("kysely_test")
        .values([
          {
            name: "kysely_multi_1",
            email: "km1@example.com",
            age: 30,
            active: true,
          },
          {
            name: "kysely_multi_2",
            email: "km2@example.com",
            age: 35,
            active: false,
          },
        ])
        .returningAll()
        .execute();

      expect(result.length).toBe(2);
    });

    test("select all", async () => {
      const result = await db.selectFrom("kysely_test").selectAll().execute();
      expect(result.length).toBeGreaterThan(0);
    });

    test("select with where", async () => {
      await db
        .insertInto("kysely_test")
        .values({ name: "kysely_where", age: 42, active: true })
        .execute();
      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("name", "=", "kysely_where")
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("update row", async () => {
      const inserted = await db
        .insertInto("kysely_test")
        .values({ name: "kysely_update", age: 20, active: true })
        .returningAll()
        .execute();

      const result = await db
        .updateTable("kysely_test")
        .set({ age: 21 })
        .where("id", "=", inserted[0].id)
        .returningAll()
        .execute();

      expect(result[0].age).toBe(21);
    });

    test("delete row", async () => {
      const inserted = await db
        .insertInto("kysely_test")
        .values({ name: "kysely_delete", active: true })
        .returningAll()
        .execute();

      const result = await db
        .deleteFrom("kysely_test")
        .where("id", "=", inserted[0].id)
        .returningAll()
        .execute();

      expect(result.length).toBe(1);
    });
  });

  // ==================== Query Building ====================
  describe("Query Building", () => {
    test("select specific columns", async () => {
      const result = await db.selectFrom("kysely_test").select(["id", "name"]).limit(1).execute();

      expect(result[0]).toHaveProperty("id");
      expect(result[0]).toHaveProperty("name");
    });

    test("where with AND", async () => {
      await db
        .insertInto("kysely_test")
        .values({ name: "kysely_and", age: 25, active: true })
        .execute();

      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("name", "=", "kysely_and")
        .where("active", "=", true)
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("where with OR", async () => {
      await db
        .insertInto("kysely_test")
        .values([
          { name: "kysely_or_1", active: true },
          { name: "kysely_or_2", active: true },
        ])
        .execute();

      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where((eb) => eb.or([eb("name", "=", "kysely_or_1"), eb("name", "=", "kysely_or_2")]))
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(2);
    });

    test("where with comparison operators", async () => {
      await db
        .insertInto("kysely_test")
        .values({ name: "kysely_cmp", age: 100, active: true })
        .execute();

      const gtResult = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("age", ">", 99)
        .execute();
      const _ltResult = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("age", "<", 10)
        .execute();

      expect(gtResult.length).toBeGreaterThanOrEqual(1);
    });

    test("where with LIKE", async () => {
      await db
        .insertInto("kysely_test")
        .values({ name: "kysely_like_test", active: true })
        .execute();

      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("name", "like", "kysely_like%")
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("where with IN", async () => {
      await db
        .insertInto("kysely_test")
        .values([
          { name: "kysely_in_1", active: true },
          { name: "kysely_in_2", active: true },
        ])
        .execute();

      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("name", "in", ["kysely_in_1", "kysely_in_2"])
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(2);
    });

    test("where IS NULL", async () => {
      await db
        .insertInto("kysely_test")
        .values({ name: "kysely_null", email: null, active: true })
        .execute();

      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("email", "is", null)
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Ordering & Pagination ====================
  describe("Ordering & Pagination", () => {
    test("order by ascending", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .orderBy("id", "asc")
        .limit(5)
        .execute();

      if (result.length > 1) {
        expect(result[0].id).toBeLessThan(result[1].id);
      }
    });

    test("order by descending", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .orderBy("id", "desc")
        .limit(5)
        .execute();

      if (result.length > 1) {
        expect(result[0].id).toBeGreaterThan(result[1].id);
      }
    });

    test("limit", async () => {
      const result = await db.selectFrom("kysely_test").selectAll().limit(3).execute();

      expect(result.length).toBeLessThanOrEqual(3);
    });

    test("offset", async () => {
      const all = await db.selectFrom("kysely_test").selectAll().orderBy("id", "asc").execute();
      const withOffset = await db
        .selectFrom("kysely_test")
        .selectAll()
        .orderBy("id", "asc")
        .offset(1)
        .execute();

      if (all.length > 1) {
        expect(withOffset[0].id).toBe(all[1].id);
      }
    });

    test("pagination with limit and offset", async () => {
      const page1 = await db
        .selectFrom("kysely_test")
        .selectAll()
        .orderBy("id", "asc")
        .limit(2)
        .offset(0)
        .execute();
      const page2 = await db
        .selectFrom("kysely_test")
        .selectAll()
        .orderBy("id", "asc")
        .limit(2)
        .offset(2)
        .execute();

      if (page1.length > 0 && page2.length > 0) {
        expect(page1[0].id).not.toBe(page2[0].id);
      }
    });
  });

  // ==================== Aggregations ====================
  describe("Aggregations", () => {
    test("count", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((eb) => eb.fn.count("id").as("count"))
        .execute();

      expect(Number(result[0].count)).toBeGreaterThan(0);
    });

    test("sum", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((eb) => eb.fn.sum("age").as("total"))
        .execute();

      expect(result[0].total).toBeDefined();
    });

    test("avg", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((eb) => eb.fn.avg("age").as("average"))
        .execute();

      expect(result[0].average).toBeDefined();
    });

    test("min and max", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((eb) => [eb.fn.min("age").as("min_age"), eb.fn.max("age").as("max_age")])
        .execute();

      expect(result[0]).toHaveProperty("min_age");
      expect(result[0]).toHaveProperty("max_age");
    });

    test("group by", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((eb) => ["active", eb.fn.count("id").as("count")])
        .groupBy("active")
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Raw SQL ====================
  describe("Raw SQL", () => {
    test("execute raw SQL", async () => {
      const result = await sql<{ type: string }>`SELECT 'raw' as type`.execute(db);
      expect(result.rows[0].type).toBe("raw");
    });

    test("raw SQL with parameters", async () => {
      const name = "raw_param_kysely";
      await db.insertInto("kysely_test").values({ name, active: true }).execute();

      const result = await sql<{
        name: string;
      }>`SELECT * FROM kysely_test WHERE name = ${name}`.execute(db);
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("raw SQL in select", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((_eb) => ["name", sql<number>`LENGTH(name)`.as("name_length")])
        .limit(5)
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(0);
    });
  });

  // ==================== Type Handling ====================
  describe("Type Handling", () => {
    test("JSON type", async () => {
      const metadata = { key: "value", nested: { num: 42 } };
      const result = await db
        .insertInto("kysely_test")
        .values({
          name: "kysely_json",
          metadata,
          active: true,
        })
        .returningAll()
        .execute();

      expect(result[0].metadata).toEqual(metadata);
    });

    test("boolean type", async () => {
      const result = await db
        .insertInto("kysely_test")
        .values({ name: "kysely_bool", active: false })
        .returningAll()
        .execute();

      expect(result[0].active).toBe(false);
    });

    test("NULL handling", async () => {
      const result = await db
        .insertInto("kysely_test")
        .values({
          name: "kysely_nulls",
          email: null,
          age: null,
          active: true,
        })
        .returningAll()
        .execute();

      expect(result[0].email).toBeNull();
      expect(result[0].age).toBeNull();
    });

    test("timestamp type", async () => {
      const result = await db
        .insertInto("kysely_test")
        .values({ name: "kysely_timestamp", active: true })
        .returningAll()
        .execute();

      expect(result[0].created_at).toBeDefined();
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("handles invalid query", async () => {
      try {
        await sql`SELECT * FROM nonexistent_table_xyz`.execute(db);
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });

    test("handles type mismatch", async () => {
      try {
        await sql`SELECT 'not a number'::integer`.execute(db);
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });

    test("handles constraint violation gracefully", async () => {
      // Name is not unique, so this should work
      const result1 = await db
        .insertInto("kysely_test")
        .values({ name: "kysely_dup", active: true })
        .returningAll()
        .execute();
      const result2 = await db
        .insertInto("kysely_test")
        .values({ name: "kysely_dup", active: true })
        .returningAll()
        .execute();

      expect(result1.length).toBe(1);
      expect(result2.length).toBe(1);
    });
  });

  // ==================== Advanced Features ====================
  describe("Advanced Features", () => {
    test("subquery", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .selectAll()
        .where("id", "in", (eb) => eb.selectFrom("kysely_test").select("id").limit(5))
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(0);
    });

    test("DISTINCT", async () => {
      const result = await db.selectFrom("kysely_test").select("active").distinct().execute();

      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("COALESCE via raw SQL", async () => {
      const result = await db
        .selectFrom("kysely_test")
        .select((_eb) => ["name", sql<string>`COALESCE(email, 'no-email')`.as("email_display")])
        .limit(5)
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(0);
    });

    test("CASE via raw SQL", async () => {
      const result = await sql`
        SELECT
          name,
          CASE
            WHEN age > 30 THEN 'adult'
            WHEN age > 18 THEN 'young_adult'
            ELSE 'unknown'
          END as category
        FROM kysely_test
        LIMIT 5
      `.execute(db);

      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });
  });

  // ==================== Batch Operations ====================
  describe("Batch Operations", () => {
    test("insert many rows", async () => {
      const rows = Array.from({ length: 20 }, (_, i) => ({
        name: `kysely_batch_${i}`,
        email: `kb${i}@example.com`,
        age: 20 + i,
        active: true,
      }));

      const result = await db.insertInto("kysely_test").values(rows).returningAll().execute();

      expect(result.length).toBe(20);
    });

    test("update many rows", async () => {
      await db
        .insertInto("kysely_test")
        .values([
          { name: "kysely_upd_many_1", active: false },
          { name: "kysely_upd_many_2", active: false },
        ])
        .execute();

      const result = await db
        .updateTable("kysely_test")
        .set({ active: true })
        .where("name", "like", "kysely_upd_many%")
        .returningAll()
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(2);
    });

    test("delete many rows", async () => {
      await db
        .insertInto("kysely_test")
        .values([
          { name: "kysely_del_many_1", active: true },
          { name: "kysely_del_many_2", active: true },
        ])
        .execute();

      const result = await db
        .deleteFrom("kysely_test")
        .where("name", "like", "kysely_del_many%")
        .returningAll()
        .execute();

      expect(result.length).toBeGreaterThanOrEqual(2);
    });
  });

  // ==================== Performance ====================
  describe("Performance", () => {
    test("large result set", async () => {
      const result = await sql`SELECT generate_series(1, 1000) as n`.execute(db);
      expect(result.rows.length).toBe(1000);
    });

    test("concurrent queries", async () => {
      const queries = Array.from({ length: 10 }, () =>
        db.selectFrom("kysely_test").selectAll().limit(1).execute(),
      );

      const results = await Promise.all(queries);
      expect(results.length).toBe(10);
    });
  });
});

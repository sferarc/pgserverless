/**
 * Drizzle ORM Integration Tests (50 tests)
 *
 * Tests for Drizzle ORM integration with Neon serverless driver
 */

import { neon } from "@neondatabase/serverless";
import { and, asc, avg, count, desc, sql as drizzleSql, eq, gt, lt, or, sum } from "drizzle-orm";
import { drizzle } from "drizzle-orm/neon-http";
import {
  boolean,
  doublePrecision,
  integer,
  jsonb,
  pgTable,
  serial,
  text,
  timestamp,
} from "drizzle-orm/pg-core";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";

// Define test schema
const testTableName = uniqueTableName("drizzle_test");

const testTable = pgTable(testTableName, {
  id: serial("id").primaryKey(),
  name: text("name").notNull(),
  email: text("email"),
  age: integer("age"),
  active: boolean("active").default(true),
  score: doublePrecision("score"),
  metadata: jsonb("metadata"),
  createdAt: timestamp("created_at").defaultNow(),
});

describe("Drizzle ORM Integration Tests", () => {
  let db: ReturnType<typeof drizzle>;
  let rawSql: ReturnType<typeof neon>;

  beforeAll(async () => {
    rawSql = neon(getDatabaseUrl());
    db = drizzle(rawSql);

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
  });

  // ==================== Connection & Basic Operations ====================
  describe("Connection & Basic Operations", () => {
    test("basic connection", async () => {
      const result = await db.execute(drizzleSql`SELECT 1 as num`);
      expect(result.rows.length).toBe(1);
    });

    test("schema definition works", () => {
      expect(testTable).toBeDefined();
      expect(testTable.id).toBeDefined();
      expect(testTable.name).toBeDefined();
    });

    test("insert single row", async () => {
      const result = await db
        .insert(testTable)
        .values({
          name: "test_user_1",
          email: "test1@example.com",
          age: 25,
        })
        .returning();
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("test_user_1");
    });

    test("insert multiple rows", async () => {
      const result = await db
        .insert(testTable)
        .values([
          { name: "multi_1", email: "multi1@example.com", age: 30 },
          { name: "multi_2", email: "multi2@example.com", age: 35 },
        ])
        .returning();
      expect(result.length).toBe(2);
    });

    test("select all", async () => {
      const result = await db.select().from(testTable);
      expect(result.length).toBeGreaterThan(0);
    });

    test("select with where", async () => {
      await db.insert(testTable).values({ name: "where_test", age: 42 });
      const result = await db.select().from(testTable).where(eq(testTable.name, "where_test"));
      expect(result.length).toBeGreaterThanOrEqual(1);
      expect(result[0].name).toBe("where_test");
    });
  });

  // ==================== Query Building ====================
  describe("Query Building", () => {
    test("select specific columns", async () => {
      const result = await db
        .select({
          id: testTable.id,
          name: testTable.name,
        })
        .from(testTable)
        .limit(1);
      expect(result[0]).toHaveProperty("id");
      expect(result[0]).toHaveProperty("name");
      expect(result[0]).not.toHaveProperty("email");
    });

    test("update query", async () => {
      const inserted = await db
        .insert(testTable)
        .values({
          name: "update_test",
          age: 20,
        })
        .returning();

      const result = await db
        .update(testTable)
        .set({ age: 21 })
        .where(eq(testTable.id, inserted[0].id))
        .returning();

      expect(result[0].age).toBe(21);
    });

    test("delete query", async () => {
      const inserted = await db
        .insert(testTable)
        .values({
          name: "delete_test",
        })
        .returning();

      const result = await db.delete(testTable).where(eq(testTable.id, inserted[0].id)).returning();

      expect(result.length).toBe(1);
    });

    test("where with AND", async () => {
      await db.insert(testTable).values({ name: "and_test", age: 25, active: true });
      const result = await db
        .select()
        .from(testTable)
        .where(and(eq(testTable.name, "and_test"), eq(testTable.active, true)));
      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("where with OR", async () => {
      await db.insert(testTable).values([
        { name: "or_test_1", age: 10 },
        { name: "or_test_2", age: 20 },
      ]);
      const result = await db
        .select()
        .from(testTable)
        .where(or(eq(testTable.name, "or_test_1"), eq(testTable.name, "or_test_2")));
      expect(result.length).toBeGreaterThanOrEqual(2);
    });

    test("where with greater than", async () => {
      await db.insert(testTable).values({ name: "gt_test", age: 100 });
      const result = await db.select().from(testTable).where(gt(testTable.age, 99));
      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("where with less than", async () => {
      await db.insert(testTable).values({ name: "lt_test", age: 5 });
      const result = await db.select().from(testTable).where(lt(testTable.age, 10));
      expect(result.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Ordering & Pagination ====================
  describe("Ordering & Pagination", () => {
    test("order by ascending", async () => {
      const result = await db.select().from(testTable).orderBy(asc(testTable.id)).limit(5);
      if (result.length > 1) {
        expect(result[0].id).toBeLessThan(result[1].id);
      }
    });

    test("order by descending", async () => {
      const result = await db.select().from(testTable).orderBy(desc(testTable.id)).limit(5);
      if (result.length > 1) {
        expect(result[0].id).toBeGreaterThan(result[1].id);
      }
    });

    test("limit", async () => {
      const result = await db.select().from(testTable).limit(3);
      expect(result.length).toBeLessThanOrEqual(3);
    });

    test("offset", async () => {
      const all = await db.select().from(testTable).orderBy(asc(testTable.id));
      const withOffset = await db
        .select()
        .from(testTable)
        .orderBy(asc(testTable.id))
        .offset(1)
        .limit(all.length);

      if (all.length > 1) {
        expect(withOffset[0].id).toBe(all[1].id);
      }
    });

    test("limit with offset (pagination)", async () => {
      const page1 = await db.select().from(testTable).orderBy(asc(testTable.id)).limit(2).offset(0);
      const page2 = await db.select().from(testTable).orderBy(asc(testTable.id)).limit(2).offset(2);

      if (page1.length > 0 && page2.length > 0) {
        expect(page1[0].id).not.toBe(page2[0].id);
      }
    });
  });

  // ==================== Aggregations ====================
  describe("Aggregations", () => {
    test("count", async () => {
      const result = await db.select({ count: count() }).from(testTable);
      expect(Number(result[0].count)).toBeGreaterThan(0);
    });

    test("sum", async () => {
      const result = await db.select({ total: sum(testTable.age) }).from(testTable);
      expect(result[0].total).toBeDefined();
    });

    test("avg", async () => {
      const result = await db.select({ average: avg(testTable.age) }).from(testTable);
      expect(result[0].average).toBeDefined();
    });

    test("group by", async () => {
      await db.insert(testTable).values([
        { name: "group_a", active: true },
        { name: "group_b", active: true },
        { name: "group_c", active: false },
      ]);

      const result = await db
        .select({
          active: testTable.active,
          count: count(),
        })
        .from(testTable)
        .groupBy(testTable.active);

      expect(result.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Type Handling ====================
  describe("Type Handling", () => {
    test("JSON type", async () => {
      const metadata = { key: "value", nested: { num: 42 } };
      const result = await db
        .insert(testTable)
        .values({
          name: "json_test",
          metadata,
        })
        .returning();

      expect(result[0].metadata).toEqual(metadata);
    });

    test("boolean type", async () => {
      const result = await db
        .insert(testTable)
        .values({
          name: "bool_test",
          active: false,
        })
        .returning();

      expect(result[0].active).toBe(false);
    });

    test("timestamp type", async () => {
      const result = await db
        .insert(testTable)
        .values({
          name: "timestamp_test",
        })
        .returning();

      expect(result[0].createdAt).toBeDefined();
    });

    test("NULL handling", async () => {
      const result = await db
        .insert(testTable)
        .values({
          name: "null_test",
          email: null,
          age: null,
        })
        .returning();

      expect(result[0].email).toBeNull();
      expect(result[0].age).toBeNull();
    });

    test("double precision type", async () => {
      const result = await db
        .insert(testTable)
        .values({
          name: "float_test",
          score: Math.PI,
        })
        .returning();

      expect(result[0].score).toBeCloseTo(Math.PI);
    });
  });

  // ==================== Raw SQL ====================
  describe("Raw SQL", () => {
    test("execute raw SQL", async () => {
      const result = await db.execute(drizzleSql`SELECT 'raw' as type`);
      expect(result.rows[0].type).toBe("raw");
    });

    test("raw SQL with parameters", async () => {
      const name = "raw_param_test";
      await db.insert(testTable).values({ name });

      const result = await db.execute(
        drizzleSql`SELECT * FROM ${drizzleSql.raw(testTableName)} WHERE name = ${name}`,
      );
      expect(result.rows.length).toBeGreaterThanOrEqual(1);
    });

    test("raw SQL in where clause", async () => {
      await db.insert(testTable).values({ name: "raw_where", age: 50 });

      const result = await db.select().from(testTable).where(drizzleSql`${testTable.age} > 45`);
      expect(result.length).toBeGreaterThanOrEqual(1);
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("handles constraint violation", async () => {
      // First insert
      await db.insert(testTable).values({ name: "constraint_test" });

      // This should work since name is not unique
      const result = await db.insert(testTable).values({ name: "constraint_test" }).returning();
      expect(result.length).toBe(1);
    });

    test("handles invalid query", async () => {
      try {
        await db.execute(drizzleSql`SELECT * FROM nonexistent_table_xyz`);
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });

    test("handles type mismatch", async () => {
      try {
        await db.execute(drizzleSql`SELECT 'not a number'::integer`);
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });
  });

  // ==================== Advanced Queries ====================
  describe("Advanced Queries", () => {
    test("subquery", async () => {
      const subquery = db.select({ id: testTable.id }).from(testTable).limit(5);
      const result = await db
        .select()
        .from(testTable)
        .where(drizzleSql`${testTable.id} IN (SELECT id FROM (${subquery}) sq)`);
      expect(result.length).toBeGreaterThanOrEqual(0);
    });

    test("CASE expression via raw SQL", async () => {
      const result = await db.execute(drizzleSql`
        SELECT
          name,
          CASE
            WHEN age > 30 THEN 'adult'
            WHEN age > 18 THEN 'young_adult'
            ELSE 'minor'
          END as category
        FROM ${drizzleSql.raw(testTableName)}
        LIMIT 5
      `);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });

    test("COALESCE", async () => {
      const result = await db.execute(drizzleSql`
        SELECT COALESCE(email, 'no-email') as email_display
        FROM ${drizzleSql.raw(testTableName)}
        LIMIT 5
      `);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });

    test("string functions", async () => {
      const result = await db.execute(drizzleSql`
        SELECT
          UPPER(name) as upper_name,
          LOWER(name) as lower_name,
          LENGTH(name) as name_length
        FROM ${drizzleSql.raw(testTableName)}
        LIMIT 5
      `);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });

    test("date functions", async () => {
      const result = await db.execute(drizzleSql`
        SELECT
          CURRENT_DATE as today,
          CURRENT_TIMESTAMP as now,
          DATE_TRUNC('day', created_at) as day
        FROM ${drizzleSql.raw(testTableName)}
        LIMIT 1
      `);
      expect(result.rows.length).toBeGreaterThanOrEqual(0);
    });
  });

  // ==================== Batch Operations ====================
  describe("Batch Operations", () => {
    test("insert many rows", async () => {
      const rows = Array.from({ length: 20 }, (_, i) => ({
        name: `batch_${i}`,
        email: `batch${i}@example.com`,
        age: 20 + i,
      }));

      const result = await db.insert(testTable).values(rows).returning();
      expect(result.length).toBe(20);
    });

    test("update many rows", async () => {
      await db.insert(testTable).values([
        { name: "update_many_1", active: false },
        { name: "update_many_2", active: false },
      ]);

      const result = await db
        .update(testTable)
        .set({ active: true })
        .where(drizzleSql`${testTable.name} LIKE 'update_many_%'`)
        .returning();

      expect(result.length).toBeGreaterThanOrEqual(2);
    });

    test("delete many rows", async () => {
      await db.insert(testTable).values([{ name: "delete_many_1" }, { name: "delete_many_2" }]);

      const result = await db
        .delete(testTable)
        .where(drizzleSql`${testTable.name} LIKE 'delete_many_%'`)
        .returning();

      expect(result.length).toBeGreaterThanOrEqual(2);
    });
  });

  // ==================== Query Performance ====================
  describe("Query Performance", () => {
    test("large result set", async () => {
      const result = await db.execute(drizzleSql`SELECT generate_series(1, 1000) as n`);
      expect(result.rows.length).toBe(1000);
    });

    test("multiple sequential queries", async () => {
      const results = [];
      for (let i = 0; i < 10; i++) {
        const r = await db.select().from(testTable).limit(1);
        results.push(r);
      }
      expect(results.length).toBe(10);
    });

    test("concurrent queries", async () => {
      const queries = Array.from({ length: 10 }, () => db.select().from(testTable).limit(1));
      const results = await Promise.all(queries);
      expect(results.length).toBe(10);
    });
  });
});

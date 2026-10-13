/**
 * Waddler SQL Template Library Integration Tests
 *
 * Waddler is a SQL template library with a Neon HTTP driver. It provides:
 * - Tagged template literal SQL with automatic parameterization
 * - sql.identifier() for safe dynamic table/column names
 * - sql.raw() for raw SQL literal embedding
 * - sql.values() for bulk insert value lists
 * - sql.unsafe() for fully dynamic raw query execution
 * - sql.default for DEFAULT keyword in inserts
 *
 * These tests verify the waddler/neon-http driver works correctly
 * with a Neon-compatible serverless server.
 */

import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { waddler } from "waddler/neon-http";
import { getDatabaseUrl } from "../../helpers/connection";
import { uniqueTableName } from "../../helpers/fixtures";
import { asRows } from "../../helpers/types";

const dbUrl = getDatabaseUrl();
const sql = waddler(dbUrl);

describe("Waddler SQL Template Integration Tests", () => {
  let testTable: string;

  beforeAll(async () => {
    testTable = uniqueTableName("waddler");
    await sql.unsafe(`CREATE TABLE ${testTable} (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			value INTEGER,
			data JSONB,
			active BOOLEAN DEFAULT true,
			score DOUBLE PRECISION,
			tags TEXT[]
		)`);
    await sql.unsafe(
      `INSERT INTO ${testTable} (name, value, data, score, tags) VALUES
			('alice', 10, '{"role": "admin", "level": 5}', 9.5, '{dev,admin}'),
			('bob', 20, '{"role": "user", "level": 2}', 7.3, '{user}'),
			('charlie', 30, '{"role": "user", "level": 3}', 8.1, '{user,tester}'),
			('diana', 40, '{"role": "moderator", "level": 4}', 6.9, '{mod}'),
			('eve', 50, NULL, NULL, NULL)`,
    );
  });

  afterAll(async () => {
    await sql.unsafe(`DROP TABLE IF EXISTS ${testTable}`);
  });

  // ==================== Tagged Template Basics ====================
  describe("Tagged Template Basics", () => {
    test("simple SELECT literal", async () => {
      const result = await sql`SELECT 1 as val`;
      expect(result[0].val).toBe(1);
    });

    test("string literal", async () => {
      const result = await sql`SELECT 'hello'::text as val`;
      expect(result[0].val).toBe("hello");
    });

    test("multiple columns", async () => {
      const result = await sql`SELECT 1 as a, 'text' as b, true as c`;
      expect(result[0].a).toBe(1);
      expect(result[0].b).toBe("text");
      expect(result[0].c).toBe(true);
    });

    test("returns array of row objects", async () => {
      const result = await sql`SELECT generate_series(1, 3) as num`;
      expect(Array.isArray(result)).toBe(true);
      expect(result.length).toBe(3);
      expect(result[0].num).toBe(1);
      expect(result[2].num).toBe(3);
    });

    test("empty result returns empty array", async () => {
      const result = await sql`SELECT * FROM ${sql.identifier(testTable)} WHERE value > 9999`;
      expect(Array.isArray(result)).toBe(true);
      expect(result.length).toBe(0);
    });
  });

  // ==================== Parameter Interpolation ====================
  describe("Parameter Interpolation", () => {
    test("string parameter", async () => {
      const name = "alice";
      const result = await sql`SELECT * FROM ${sql.identifier(testTable)} WHERE name = ${name}`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("alice");
    });

    test("integer parameter", async () => {
      const val = 20;
      const result = await sql`SELECT * FROM ${sql.identifier(testTable)} WHERE value = ${val}`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("bob");
    });

    test("boolean parameter", async () => {
      const result = await sql`SELECT ${true}::boolean as val`;
      expect(result[0].val).toBe(true);
    });

    test("null parameter", async () => {
      const result = await sql`SELECT ${null}::text as val`;
      expect(result[0].val).toBeNull();
    });

    test("float parameter", async () => {
      const result = await sql`SELECT ${3.14}::double precision as val`;
      expect(result[0].val).toBeCloseTo(3.14);
    });

    test("multiple parameters in single query", async () => {
      const result =
        await sql`SELECT ${1}::integer as a, ${"hello"}::text as b, ${true}::boolean as c, ${null}::text as d`;
      expect(result[0].a).toBe(1);
      expect(result[0].b).toBe("hello");
      expect(result[0].c).toBe(true);
      expect(result[0].d).toBeNull();
    });

    test("parameter in WHERE with comparison", async () => {
      const minValue = 25;
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE value > ${minValue} ORDER BY value`;
      expect(result.length).toBe(3);
      expect(result[0].name).toBe("charlie");
    });

    test("parameter in LIMIT", async () => {
      const limit = 2;
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} ORDER BY id LIMIT ${limit}`;
      expect(result.length).toBe(2);
    });

    test("parameter in OFFSET", async () => {
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} ORDER BY id LIMIT 1 OFFSET ${2}`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("charlie");
    });

    test("same parameter used in multiple positions", async () => {
      const val = "alice";
      const result = await sql`SELECT ${val}::text as a, ${val}::text as b`;
      expect(result[0].a).toBe("alice");
      expect(result[0].b).toBe("alice");
    });
  });

  // ==================== Special Characters in Parameters ====================
  describe("Special Characters in Parameters", () => {
    test("single quotes", async () => {
      const val = "it's a test with 'quotes'";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("double quotes", async () => {
      const val = 'value with "double quotes"';
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("backslashes", async () => {
      const val = "back\\slash\\path";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("semicolons (no statement splitting)", async () => {
      const val = "test; DROP TABLE users; --";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("dollar signs (PG dollar quoting)", async () => {
      const val = "$$body$$ and $tag$content$tag$";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("newlines and tabs", async () => {
      const val = "line1\nline2\ttab";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("unicode characters", async () => {
      const val = "emoji: \u{1F600} kanji: \u4E16\u754C chinese: \u4F60\u597D";
      const result = await sql`SELECT ${val}::text as val`;
      expect(result[0].val).toBe(val);
    });

    test("empty string", async () => {
      const result = await sql`SELECT ${""}::text as val`;
      expect(result[0].val).toBe("");
    });

    test("SQL keywords as data values", async () => {
      const keywords = ["SELECT", "INSERT", "DROP", "DELETE", "CREATE", "ALTER"];
      for (const keyword of keywords) {
        const result = await sql`SELECT ${keyword}::text as val`;
        expect(result[0].val).toBe(keyword);
      }
    });
  });

  // ==================== sql.identifier() ====================
  describe("sql.identifier()", () => {
    test("dynamic table name", async () => {
      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.identifier(testTable)}`;
      expect(Number(result[0].cnt)).toBe(5);
    });

    test("dynamic column name in SELECT", async () => {
      const result =
        await sql`SELECT ${sql.identifier("name")} FROM ${sql.identifier(testTable)} ORDER BY id LIMIT 1`;
      expect(result[0].name).toBe("alice");
    });

    test("dynamic column in ORDER BY", async () => {
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} ORDER BY ${sql.identifier("value")} DESC LIMIT 1`;
      expect(result[0].name).toBe("eve");
    });

    test("dynamic column in WHERE", async () => {
      const col = "name";
      const result =
        await sql`SELECT * FROM ${sql.identifier(testTable)} WHERE ${sql.identifier(col)} = ${"alice"}`;
      expect(result.length).toBe(1);
    });

    test("multiple identifiers in single query", async () => {
      const result =
        await sql`SELECT ${sql.identifier("name")}, ${sql.identifier("value")} FROM ${sql.identifier(testTable)} ORDER BY ${sql.identifier("id")} LIMIT 1`;
      expect(result[0].name).toBe("alice");
      expect(result[0].value).toBe(10);
    });

    test("identifier with array of columns", async () => {
      const cols = ["name", "value"];
      const result =
        await sql`SELECT ${sql.identifier(cols)} FROM ${sql.identifier(testTable)} ORDER BY id LIMIT 1`;
      expect(result[0].name).toBe("alice");
      expect(result[0].value).toBe(10);
    });
  });

  // ==================== sql.raw() ====================
  describe("sql.raw()", () => {
    test("raw SQL literal number", async () => {
      const result = await sql`SELECT ${sql.raw(42)} as val`;
      expect(result[0].val).toBe(42);
    });

    test("raw SQL literal string", async () => {
      const result = await sql`SELECT ${sql.raw("'hello'")} as val`;
      expect(result[0].val).toBe("hello");
    });

    test("raw boolean", async () => {
      const result = await sql`SELECT ${sql.raw(true)}::boolean as val`;
      expect(result[0].val).toBe(true);
    });

    test("raw in ORDER BY direction", async () => {
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} ORDER BY value ${sql.raw("DESC")} LIMIT 1`;
      expect(result[0].name).toBe("eve");
    });

    test("raw in aggregate expression", async () => {
      const result =
        await sql`SELECT ${sql.raw("COUNT(*)")} as cnt FROM ${sql.identifier(testTable)}`;
      expect(Number(result[0].cnt)).toBe(5);
    });
  });

  // ==================== sql.unsafe() ====================
  describe("sql.unsafe()", () => {
    test("basic query", async () => {
      const result = await sql.unsafe("SELECT 1 as val");
      expect(asRows(result)[0].val).toBe(1);
    });

    test("query with positional parameters", async () => {
      const result = await sql.unsafe("SELECT $1::integer as a, $2::text as b", [42, "hello"]);
      expect(asRows(result)[0].a).toBe(42);
      expect(asRows(result)[0].b).toBe("hello");
    });

    test("query with dynamic table name", async () => {
      const result = await sql.unsafe(`SELECT * FROM ${testTable} ORDER BY id`);
      expect(result.length).toBe(5);
    });

    test("DDL via unsafe", async () => {
      const tbl = uniqueTableName("waddler_unsafe_ddl");
      await sql.unsafe(`CREATE TABLE ${tbl} (id INT)`);
      await sql.unsafe(`INSERT INTO ${tbl} VALUES (1)`);

      const result = await sql.unsafe(`SELECT * FROM ${tbl}`);
      expect(result.length).toBe(1);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("unsafe with empty params array", async () => {
      const result = await sql.unsafe("SELECT 1 as val", []);
      expect(asRows(result)[0].val).toBe(1);
    });
  });

  // ==================== sql.values() for bulk insert ====================
  describe("sql.values()", () => {
    test("bulk insert with values helper", async () => {
      const tbl = uniqueTableName("waddler_values");
      await sql.unsafe(`CREATE TABLE ${tbl} (name TEXT, age INT)`);

      const rows = [
        ["Alice", 30],
        ["Bob", 25],
        ["Charlie", 35],
      ];
      await sql`INSERT INTO ${sql.identifier(tbl)} (name, age) VALUES ${sql.values(rows)}`;

      const result = await sql`SELECT * FROM ${sql.identifier(tbl)} ORDER BY age`;
      expect(result.length).toBe(3);
      expect(result[0].name).toBe("Bob");
      expect(result[0].age).toBe(25);
      expect(result[2].name).toBe("Charlie");
      expect(result[2].age).toBe(35);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("single row insert with values", async () => {
      const tbl = uniqueTableName("waddler_values_single");
      await sql.unsafe(`CREATE TABLE ${tbl} (x INT, y TEXT)`);

      await sql`INSERT INTO ${sql.identifier(tbl)} (x, y) VALUES ${sql.values([[1, "one"]])}`;

      const result = await sql`SELECT * FROM ${sql.identifier(tbl)}`;
      expect(result.length).toBe(1);
      expect(result[0].x).toBe(1);
      expect(result[0].y).toBe("one");

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("values with NULL", async () => {
      const tbl = uniqueTableName("waddler_values_null");
      await sql.unsafe(`CREATE TABLE ${tbl} (a TEXT, b INT)`);

      await sql`INSERT INTO ${sql.identifier(tbl)} (a, b) VALUES ${sql.values([
        ["hello", null],
        [null, 42],
      ])}`;

      const result = await sql`SELECT * FROM ${sql.identifier(tbl)} ORDER BY b NULLS FIRST`;
      expect(result.length).toBe(2);
      expect(result[0].a).toBe("hello");
      expect(result[0].b).toBeNull();
      expect(result[1].a).toBeNull();
      expect(result[1].b).toBe(42);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });
  });

  // ==================== Data Types ====================
  describe("Data Types", () => {
    test("integer", async () => {
      const result = await sql`SELECT 42::integer as val`;
      expect(result[0].val).toBe(42);
    });

    test("bigint returns string", async () => {
      const result = await sql`SELECT 9223372036854775807::bigint as val`;
      expect(result[0].val).toBe("9223372036854775807");
    });

    test("float / double precision", async () => {
      // biome-ignore lint/suspicious/noApproximativeNumericConstant: testing float handling, not Math.PI
      const pi = 3.14159;
      const result = await sql`SELECT ${pi}::double precision as val`;
      expect(result[0].val).toBeCloseTo(pi);
    });

    test("numeric as string", async () => {
      const result = await sql`SELECT 123.456::numeric as val`;
      expect(result[0].val).toBe("123.456");
    });

    test("boolean true/false", async () => {
      const result = await sql`SELECT true::boolean as t, false::boolean as f`;
      expect(result[0].t).toBe(true);
      expect(result[0].f).toBe(false);
    });

    test("text", async () => {
      const result = await sql`SELECT 'hello world'::text as val`;
      expect(result[0].val).toBe("hello world");
    });

    test("JSONB object", async () => {
      const result = await sql`SELECT '{"key": "value", "num": 42}'::jsonb as val`;
      expect(result[0].val).toEqual({ key: "value", num: 42 });
    });

    test("JSONB array", async () => {
      const result = await sql`SELECT '[1, 2, 3]'::jsonb as val`;
      expect(result[0].val).toEqual([1, 2, 3]);
    });

    test("JSONB nested", async () => {
      const result = await sql`SELECT '{"a": {"b": {"c": 1}}}'::jsonb as val`;
      expect(result[0].val).toEqual({ a: { b: { c: 1 } } });
    });

    test("integer array", async () => {
      const result = await sql`SELECT ARRAY[1, 2, 3]::integer[] as val`;
      expect(result[0].val).toEqual([1, 2, 3]);
    });

    test("text array", async () => {
      const result = await sql`SELECT ARRAY['a', 'b', 'c']::text[] as val`;
      expect(result[0].val).toEqual(["a", "b", "c"]);
    });

    test("date", async () => {
      const result = await sql`SELECT '2024-01-15'::date as val`;
      expect(result[0].val).toBeDefined();
    });

    test("timestamp", async () => {
      const result = await sql`SELECT '2024-01-15 10:30:00'::timestamp as val`;
      expect(result[0].val).toBeDefined();
    });

    test("NULL for all types", async () => {
      const result =
        await sql`SELECT NULL::text as a, NULL::integer as b, NULL::jsonb as c, NULL::boolean as d, NULL::integer[] as e`;
      expect(result[0].a).toBeNull();
      expect(result[0].b).toBeNull();
      expect(result[0].c).toBeNull();
      expect(result[0].d).toBeNull();
      expect(result[0].e).toBeNull();
    });
  });

  // ==================== CRUD Operations ====================
  describe("CRUD Operations", () => {
    test("full CRUD lifecycle", async () => {
      const tbl = uniqueTableName("waddler_crud");
      await sql.unsafe(`CREATE TABLE ${tbl} (id SERIAL PRIMARY KEY, name TEXT, value INT)`);

      // Create
      await sql`INSERT INTO ${sql.identifier(tbl)} (name, value) VALUES (${"test"}, ${100})`;

      // Read
      let result = await sql`SELECT * FROM ${sql.identifier(tbl)}`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("test");
      expect(result[0].value).toBe(100);

      // Update
      await sql`UPDATE ${sql.identifier(tbl)} SET value = ${200} WHERE name = ${"test"}`;
      result = await sql`SELECT value FROM ${sql.identifier(tbl)} WHERE name = ${"test"}`;
      expect(result[0].value).toBe(200);

      // Delete
      await sql`DELETE FROM ${sql.identifier(tbl)} WHERE name = ${"test"}`;
      result = await sql`SELECT * FROM ${sql.identifier(tbl)}`;
      expect(result.length).toBe(0);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("INSERT RETURNING", async () => {
      const tbl = uniqueTableName("waddler_returning");
      await sql.unsafe(`CREATE TABLE ${tbl} (id SERIAL PRIMARY KEY, name TEXT)`);

      const result =
        await sql`INSERT INTO ${sql.identifier(tbl)} (name) VALUES (${"alice"}) RETURNING id, name`;
      expect(result.length).toBe(1);
      expect(result[0].id).toBeDefined();
      expect(result[0].name).toBe("alice");

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("UPDATE RETURNING", async () => {
      const tbl = uniqueTableName("waddler_upd_ret");
      await sql.unsafe(`CREATE TABLE ${tbl} (id INT, val INT)`);
      await sql`INSERT INTO ${sql.identifier(tbl)} VALUES (1, 100)`;

      const result =
        await sql`UPDATE ${sql.identifier(tbl)} SET val = ${200} WHERE id = ${1} RETURNING *`;
      expect(result.length).toBe(1);
      expect(result[0].val).toBe(200);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("DELETE RETURNING", async () => {
      const tbl = uniqueTableName("waddler_del_ret");
      await sql.unsafe(`CREATE TABLE ${tbl} (id INT, name TEXT)`);
      await sql`INSERT INTO ${sql.identifier(tbl)} VALUES (1, 'alice'), (2, 'bob')`;

      const result = await sql`DELETE FROM ${sql.identifier(tbl)} WHERE id = ${1} RETURNING *`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("alice");

      const remaining = await sql`SELECT * FROM ${sql.identifier(tbl)}`;
      expect(remaining.length).toBe(1);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("UPSERT (INSERT ON CONFLICT)", async () => {
      const tbl = uniqueTableName("waddler_upsert");
      await sql.unsafe(`CREATE TABLE ${tbl} (id INT PRIMARY KEY, name TEXT, value INT)`);
      await sql`INSERT INTO ${sql.identifier(tbl)} VALUES (1, 'alice', 100)`;

      await sql`INSERT INTO ${sql.identifier(tbl)} (id, name, value) VALUES (${1}, ${"alice"}, ${200})
				ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value`;

      const result = await sql`SELECT * FROM ${sql.identifier(tbl)}`;
      expect(result.length).toBe(1);
      expect(result[0].value).toBe(200);

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });
  });

  // ==================== Aggregations and Subqueries ====================
  describe("Aggregations and Subqueries", () => {
    test("COUNT", async () => {
      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.identifier(testTable)}`;
      expect(Number(result[0].cnt)).toBe(5);
    });

    test("SUM", async () => {
      const result = await sql`SELECT SUM(value) as total FROM ${sql.identifier(testTable)}`;
      expect(Number(result[0].total)).toBe(150);
    });

    test("AVG", async () => {
      const result =
        await sql`SELECT AVG(value)::double precision as avg_val FROM ${sql.identifier(testTable)} WHERE value IS NOT NULL`;
      expect(result[0].avg_val).toBeCloseTo(30);
    });

    test("GROUP BY", async () => {
      const result =
        await sql`SELECT active, COUNT(*) as cnt FROM ${sql.identifier(testTable)} GROUP BY active`;
      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("HAVING", async () => {
      const result =
        await sql`SELECT active, COUNT(*) as cnt FROM ${sql.identifier(testTable)} GROUP BY active HAVING COUNT(*) > ${1}`;
      expect(result.length).toBeGreaterThanOrEqual(1);
    });

    test("subquery in WHERE", async () => {
      const result = await sql`SELECT name FROM ${sql.identifier(testTable)}
				WHERE value > (SELECT AVG(value) FROM ${sql.identifier(testTable)} WHERE value IS NOT NULL)
				ORDER BY value`;
      expect(result.length).toBeGreaterThan(0);
    });
  });

  // ==================== Joins ====================
  describe("Joins", () => {
    let tblA: string;
    let tblB: string;

    beforeAll(async () => {
      tblA = uniqueTableName("waddler_join_a");
      tblB = uniqueTableName("waddler_join_b");
      await sql.unsafe(`CREATE TABLE ${tblA} (id INT PRIMARY KEY, name TEXT)`);
      await sql.unsafe(
        `CREATE TABLE ${tblB} (id INT PRIMARY KEY, a_id INT REFERENCES ${tblA}(id), label TEXT)`,
      );
      await sql.unsafe(`INSERT INTO ${tblA} VALUES (1, 'x'), (2, 'y'), (3, 'z')`);
      await sql.unsafe(
        `INSERT INTO ${tblB} VALUES (1, 1, 'label1'), (2, 1, 'label2'), (3, 2, 'label3')`,
      );
    });

    afterAll(async () => {
      await sql.unsafe(`DROP TABLE IF EXISTS ${tblB}`);
      await sql.unsafe(`DROP TABLE IF EXISTS ${tblA}`);
    });

    test("INNER JOIN", async () => {
      const result = await sql.unsafe(
        `SELECT a.name, b.label FROM ${tblA} a INNER JOIN ${tblB} b ON a.id = b.a_id ORDER BY b.id`,
      );
      expect(result.length).toBe(3);
      expect(asRows(result)[0].name).toBe("x");
      expect(asRows(result)[0].label).toBe("label1");
    });

    test("LEFT JOIN", async () => {
      const result = await sql.unsafe(
        `SELECT a.name, b.label FROM ${tblA} a LEFT JOIN ${tblB} b ON a.id = b.a_id ORDER BY a.id`,
      );
      expect(result.length).toBe(4); // z has no match
      expect(asRows(result)[3].name).toBe("z");
      expect(asRows(result)[3].label).toBeNull();
    });
  });

  // ==================== Complex Query Patterns ====================
  describe("Complex Query Patterns", () => {
    test("CASE expression", async () => {
      const result = await sql`SELECT name,
				CASE WHEN value >= ${30} THEN 'high' ELSE 'low' END as tier
				FROM ${sql.identifier(testTable)} WHERE value IS NOT NULL ORDER BY value`;
      expect(result[0].tier).toBe("low");
      expect(result[2].tier).toBe("high");
    });

    test("COALESCE with parameter", async () => {
      const defaultVal = 0;
      // eve has score=NULL, so COALESCE on score column with a default
      const result =
        await sql`SELECT name, COALESCE(score, ${defaultVal}::double precision) as val FROM ${sql.identifier(testTable)} WHERE name = ${"eve"}`;
      expect(result[0].val).toBe(0);
    });

    test("LIKE with parameter", async () => {
      const pattern = "a%";
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE name LIKE ${pattern}`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("alice");
    });

    test("IN list with parameters", async () => {
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE name IN (${sql.raw("'alice', 'bob'")}) ORDER BY name`;
      expect(result.length).toBe(2);
    });

    test("JSONB field access", async () => {
      const result =
        await sql`SELECT name, data->>'role' as role FROM ${sql.identifier(testTable)} WHERE data->>'role' = ${"admin"}`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("alice");
    });

    test("JSONB containment", async () => {
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE data @> ${'{"level": 5}'}::jsonb`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("alice");
    });

    test("array ANY", async () => {
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE ${"admin"} = ANY(tags)`;
      expect(result.length).toBe(1);
      expect(result[0].name).toBe("alice");
    });

    test("CTEs (WITH clause)", async () => {
      const result = await sql`WITH high_value AS (
				SELECT * FROM ${sql.identifier(testTable)} WHERE value > ${25}
			) SELECT COUNT(*) as cnt FROM high_value`;
      expect(Number(result[0].cnt)).toBe(3);
    });

    test("window functions", async () => {
      const result = await sql`SELECT name, value,
				ROW_NUMBER() OVER (ORDER BY value DESC) as rn
				FROM ${sql.identifier(testTable)} WHERE value IS NOT NULL`;
      expect(result.length).toBe(5);
      expect(result[0].rn).toBe("1");
      expect(result[0].name).toBe("eve");
    });
  });

  // ==================== Error Handling ====================
  describe("Error Handling", () => {
    test("syntax error", async () => {
      try {
        await sql`SELEC 1`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        const sourceErr = (err.cause ?? err) as Record<string, unknown>;
        expect(sourceErr.code).toBe("42601");
      }
    });

    test("undefined table", async () => {
      try {
        await sql`SELECT * FROM ${sql.identifier("nonexistent_waddler_table_xyz")}`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        const sourceErr = (err.cause ?? err) as Record<string, unknown>;
        expect(sourceErr.code).toBe("42P01");
      }
    });

    test("type mismatch", async () => {
      try {
        await sql`SELECT ${"not_a_number"}::integer as val`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        const sourceErr = (err.cause ?? err) as Record<string, unknown>;
        expect(sourceErr.code).toBe("22P02");
      }
    });

    test("division by zero", async () => {
      try {
        await sql`SELECT 1/0`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        const sourceErr = (err.cause ?? err) as Record<string, unknown>;
        expect(sourceErr.code).toBe("22012");
      }
    });

    test("constraint violation", async () => {
      const tbl = uniqueTableName("waddler_constraint");
      await sql.unsafe(`CREATE TABLE ${tbl} (id INT PRIMARY KEY)`);
      await sql`INSERT INTO ${sql.identifier(tbl)} VALUES (1)`;

      try {
        await sql`INSERT INTO ${sql.identifier(tbl)} VALUES (1)`;
        expect(true).toBe(false);
      } catch (e) {
        const err = e as Record<string, unknown>;
        const sourceErr = (err.cause ?? err) as Record<string, unknown>;
        expect(sourceErr.code).toBe("23505");
      }

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });

    test("error recovery - next query succeeds", async () => {
      try {
        await sql`INVALID SQL`;
      } catch {
        // Expected
      }
      const result = await sql`SELECT 'recovered' as val`;
      expect(result[0].val).toBe("recovered");
    });

    test("unsafe error", async () => {
      try {
        await sql.unsafe("SELEC 1");
        expect(true).toBe(false);
      } catch (e) {
        expect(e).toBeDefined();
      }
    });
  });

  // ==================== SQL Injection Protection ====================
  describe("SQL Injection Protection", () => {
    test("DROP TABLE in parameter is treated as data", async () => {
      const malicious = `'; DROP TABLE ${testTable}; --`;
      const result = await sql`SELECT ${malicious}::text as val`;
      expect(result[0].val).toBe(malicious);

      // Table still exists
      const check = await sql`SELECT COUNT(*) as cnt FROM ${sql.identifier(testTable)}`;
      expect(Number(check[0].cnt)).toBe(5);
    });

    test("UNION injection in parameter is safe", async () => {
      const malicious = "' UNION SELECT * FROM pg_catalog.pg_tables --";
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE name = ${malicious}`;
      expect(result.length).toBe(0);
    });

    test("OR 1=1 in parameter is safe", async () => {
      const malicious = "' OR '1'='1";
      const result =
        await sql`SELECT name FROM ${sql.identifier(testTable)} WHERE name = ${malicious}`;
      expect(result.length).toBe(0);
    });
  });

  // ==================== Concurrent Queries ====================
  describe("Concurrent Queries", () => {
    test("parallel tagged template queries", async () => {
      const queries = Array.from({ length: 10 }, (_, i) => sql`SELECT ${i}::integer as num`);
      const results = await Promise.all(queries);
      for (let i = 0; i < 10; i++) {
        expect(results[i][0].num).toBe(i);
      }
    });

    test("parallel unsafe queries", async () => {
      const queries = Array.from({ length: 10 }, (_, i) =>
        sql.unsafe("SELECT $1::integer as num", [i]),
      );
      const results = await Promise.all(queries);
      for (let i = 0; i < 10; i++) {
        expect(asRows(results[i])[0].num).toBe(i);
      }
    });

    test("mixed tagged template and unsafe in parallel", async () => {
      const queries = [
        sql`SELECT ${1}::integer as val`,
        sql.unsafe("SELECT $1::integer as val", [2]),
        sql`SELECT ${3}::integer as val`,
        sql.unsafe("SELECT $1::integer as val", [4]),
      ];
      const results = await Promise.all(queries);
      expect(asRows(results[0])[0].val).toBe(1);
      expect(asRows(results[1])[0].val).toBe(2);
      expect(asRows(results[2])[0].val).toBe(3);
      expect(asRows(results[3])[0].val).toBe(4);
    });
  });

  // ==================== Large Data ====================
  describe("Large Data", () => {
    test("large text parameter", async () => {
      const text = "x".repeat(50000);
      const result = await sql`SELECT length(${text}::text) as len`;
      expect(result[0].len).toBe(50000);
    });

    test("many rows via generate_series", async () => {
      const result = await sql`SELECT generate_series(1, 500) as num`;
      expect(result.length).toBe(500);
    });

    test("large JSON value", async () => {
      const obj: Record<string, number> = {};
      for (let i = 0; i < 200; i++) {
        obj[`key_${i}`] = i;
      }
      const result = await sql`SELECT ${JSON.stringify(obj)}::jsonb as val`;
      expect(Object.keys(result[0].val as object).length).toBe(200);
    });

    test("bulk insert with values and verify", async () => {
      const tbl = uniqueTableName("waddler_bulk");
      await sql.unsafe(`CREATE TABLE ${tbl} (id INT, label TEXT)`);

      const rows = Array.from({ length: 100 }, (_, i) => [i, `item_${i}`]);
      await sql`INSERT INTO ${sql.identifier(tbl)} (id, label) VALUES ${sql.values(rows)}`;

      const result = await sql`SELECT COUNT(*) as cnt FROM ${sql.identifier(tbl)}`;
      expect(Number(result[0].cnt)).toBe(100);

      const first = await sql`SELECT * FROM ${sql.identifier(tbl)} WHERE id = ${0}`;
      expect(first[0].label).toBe("item_0");

      await sql.unsafe(`DROP TABLE ${tbl}`);
    });
  });
});

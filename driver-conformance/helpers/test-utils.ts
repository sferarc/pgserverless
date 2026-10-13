/**
 * Shared test utilities for parallel test execution
 */

import { Client, neon, Pool } from "@neondatabase/serverless";
import { getDatabaseUrl } from "./connection";
import { uniqueTableName } from "./fixtures";

const dbUrl = getDatabaseUrl();

/**
 * Get a configured WebSocket client
 */
export async function getClient(): Promise<Client> {
  const client = new Client(dbUrl);
  await client.connect();
  return client;
}

/**
 * Get a configured pool
 */
export function getPool(options?: { max?: number }): Pool {
  return new Pool({
    connectionString: dbUrl,
    max: options?.max ?? 10,
  });
}

/**
 * Get HTTP SQL function
 */
export function getSql<
  ArrayMode extends boolean = false,
  FullResults extends boolean = false,
>(options?: { arrayMode?: ArrayMode; fullResults?: FullResults }) {
  return neon(dbUrl, options);
}

/**
 * Create a test table and return cleanup function
 */
export async function withTestTable(
  prefix: string = "test",
): Promise<{ tableName: string; cleanup: () => Promise<void> }> {
  const tableName = uniqueTableName(prefix);
  const sql = getSql();

  await sql`
    CREATE TABLE ${sql.unsafe(tableName)} (
      id SERIAL PRIMARY KEY,
      name TEXT,
      value INTEGER
    )
  `;

  return {
    tableName,
    cleanup: async () => {
      await sql`DROP TABLE IF EXISTS ${sql.unsafe(tableName)}`;
    },
  };
}

/**
 * Run a test with automatic client cleanup
 */
export async function withClient<T>(fn: (client: Client) => Promise<T>): Promise<T> {
  const client = await getClient();
  try {
    return await fn(client);
  } finally {
    await client.end();
  }
}

/**
 * Run a test with automatic pool cleanup
 */
export async function withPool<T>(
  fn: (pool: Pool) => Promise<T>,
  options?: { max?: number },
): Promise<T> {
  const pool = getPool(options);
  try {
    return await fn(pool);
  } finally {
    await pool.end();
  }
}

export { dbUrl, uniqueTableName };

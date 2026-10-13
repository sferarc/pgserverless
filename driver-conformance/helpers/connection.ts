/**
 * Connection utilities for the test suite
 *
 * Provides factory functions for creating database connections.
 * Uses DATABASE_URL, which must point at the server under test: a pgserverless
 * server, or a Neon database for a parity run.
 */

import {
  Client,
  type HTTPTransactionOptions,
  type NeonQueryFunction,
  neon,
  neonConfig,
  Pool,
} from "@neondatabase/serverless";
import ws from "ws";

// Plain HTTP and WS for a local server, which has no certificate to present
const useTls = process.env.PGSERVERLESS_TLS !== "false";

export function endpointPort(urlPort?: string | number): string {
  return process.env.PORT || String(urlPort || (useTls ? 443 : 80));
}

export function fetchEndpointFor(host: string, port?: string | number): string {
  return `${useTls ? "https" : "http"}://${host}:${endpointPort(port)}/sql`;
}

// Configure neon
neonConfig.webSocketConstructor = ws;
neonConfig.useSecureWebSocket = useTls;
neonConfig.pipelineConnect = process.env.PIPELINE_CONNECT === "false" ? false : "password";
neonConfig.fetchEndpoint = fetchEndpointFor;
neonConfig.wsProxy = (host, port) => `${host}:${endpointPort(port)}/v2`;

// Environment variables
const DATABASE_URL = process.env.DATABASE_URL;

export function getDatabaseUrl(): string {
  if (!DATABASE_URL) {
    throw new Error(
      "DATABASE_URL is required. Set it to the connection string of the server under test.",
    );
  }
  return DATABASE_URL;
}

// Connection cache
let cachedSql: NeonQueryFunction<false, false> | null = null;
let cachedClient: Client | null = null;
let cachedPool: Pool | null = null;

/**
 * Get or create an HTTP SQL connection using the neon() function
 */
export function getConnection<
  ArrayMode extends boolean = false,
  FullResults extends boolean = false,
>(
  options?: HTTPTransactionOptions<ArrayMode, FullResults>,
): NeonQueryFunction<ArrayMode, FullResults> {
  const dbUrl = getDatabaseUrl();
  return neon<ArrayMode, FullResults>(dbUrl, options);
}

/**
 * Get a cached HTTP SQL connection for simple queries
 */
export function getCachedConnection(): NeonQueryFunction<false, false> {
  if (!cachedSql) {
    cachedSql = getConnection();
  }
  return cachedSql;
}

/**
 * Create a new WebSocket Client connection
 */
export async function createClient(): Promise<Client> {
  const dbUrl = getDatabaseUrl();
  const client = new Client(dbUrl);
  await client.connect();
  return client;
}

/**
 * Get or create a cached WebSocket Client connection
 */
export async function getCachedClient(): Promise<Client> {
  if (!cachedClient) {
    cachedClient = await createClient();
  }
  return cachedClient;
}

/**
 * Create a new connection Pool
 */
export function createPool(maxConnections: number = 10): Pool {
  const dbUrl = getDatabaseUrl();
  return new Pool({
    connectionString: dbUrl,
    max: maxConnections,
  });
}

/**
 * Get or create a cached Pool
 */
export function getCachedPool(): Pool {
  if (!cachedPool) {
    cachedPool = createPool();
  }
  return cachedPool;
}

/**
 * Close all cached connections
 */
export async function closeAllConnections(): Promise<void> {
  const closePromises: Promise<void>[] = [];

  if (cachedClient) {
    closePromises.push(cachedClient.end().then(() => {}));
    cachedClient = null;
  }

  if (cachedPool) {
    closePromises.push(cachedPool.end().then(() => {}));
    cachedPool = null;
  }

  cachedSql = null;

  await Promise.all(closePromises);
}

/**
 * Parse connection string to extract components
 */
export function parseConnectionString(url: string): {
  host: string;
  port: number;
  database: string;
  user: string;
  password: string;
  ssl: boolean;
} {
  const parsed = new URL(url);
  return {
    host: parsed.hostname,
    port: Number.parseInt(parsed.port, 10) || 5432,
    database: parsed.pathname.slice(1),
    user: parsed.username,
    password: decodeURIComponent(parsed.password),
    ssl: parsed.searchParams.get("sslmode") !== "disable",
  };
}

/**
 * Generate a unique table name for test isolation
 */
export function generateTestTableName(prefix: string = "test"): string {
  const timestamp = Date.now();
  const random = Math.random().toString(36).substring(2, 8);
  return `${prefix}_${timestamp}_${random}`;
}

/**
 * Create a test table and return cleanup function
 */
export async function withTestTable(
  sql: NeonQueryFunction<boolean, boolean>,
  tableName: string,
  schema: string,
): Promise<() => Promise<void>> {
  await sql`CREATE TABLE IF NOT EXISTS ${sql.unsafe(tableName)} (${sql.unsafe(schema)})`;
  return async () => {
    await sql`DROP TABLE IF EXISTS ${sql.unsafe(tableName)}`;
  };
}

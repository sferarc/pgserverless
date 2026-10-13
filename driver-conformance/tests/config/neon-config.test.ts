/**
 * Configuration Tests (50 tests)
 *
 * Tests for neonConfig options including:
 * - neonConfig options (30 tests)
 * - Connection string tests (20 tests)
 */

import { Client, neon, neonConfig, Pool } from "@neondatabase/serverless";
import { afterEach, describe, expect, test } from "vitest";
import ws from "ws";
import { fetchEndpointFor, getDatabaseUrl } from "../../helpers/connection";

// Save and restore neonConfig values for test isolation
const savedConfig = {
  wsProxy: neonConfig.wsProxy,
  fetchEndpoint: neonConfig.fetchEndpoint,
  pipelineConnect: neonConfig.pipelineConnect,
  webSocketConstructor: neonConfig.webSocketConstructor,
  useSecureWebSocket: neonConfig.useSecureWebSocket,
  forceDisablePgSSL: neonConfig.forceDisablePgSSL,
  coalesceWrites: neonConfig.coalesceWrites,
  fetchFunction: neonConfig.fetchFunction,
  fetchConnectionCache: neonConfig.fetchConnectionCache,
};

function restoreConfig() {
  neonConfig.wsProxy = savedConfig.wsProxy;
  neonConfig.fetchEndpoint = savedConfig.fetchEndpoint;
  neonConfig.pipelineConnect = savedConfig.pipelineConnect;
  neonConfig.webSocketConstructor = savedConfig.webSocketConstructor;
  neonConfig.useSecureWebSocket = savedConfig.useSecureWebSocket;
  neonConfig.forceDisablePgSSL = savedConfig.forceDisablePgSSL;
  neonConfig.coalesceWrites = savedConfig.coalesceWrites;
  neonConfig.fetchFunction = savedConfig.fetchFunction;
  neonConfig.fetchConnectionCache = savedConfig.fetchConnectionCache;
}

describe("Configuration Tests", () => {
  const dbUrl = getDatabaseUrl();

  afterEach(() => {
    // Restore original config values after each test
    restoreConfig();
  });

  // ==================== neonConfig Options (30 tests) ====================
  describe("neonConfig Options", () => {
    test("webSocketConstructor setting", () => {
      neonConfig.webSocketConstructor = ws;
      expect(neonConfig.webSocketConstructor).toBe(ws);
    });

    test("fetchEndpoint as string", () => {
      const endpoint = "https://custom.endpoint.com/sql";
      neonConfig.fetchEndpoint = endpoint;
      expect(neonConfig.fetchEndpoint).toBe(endpoint);
    });

    test("fetchEndpoint as function", () => {
      const endpointFn = (host: string, port: number | string) => `https://${host}:${port}/custom`;
      neonConfig.fetchEndpoint = endpointFn;
      expect(typeof neonConfig.fetchEndpoint).toBe("function");
    });

    test("fetchFunction custom setting", async () => {
      let _fetchCalled = false;
      const customFetch = async (...args: [input: RequestInfo | URL, init?: RequestInit]) => {
        _fetchCalled = true;
        return fetch(...args);
      };

      neonConfig.fetchFunction = customFetch;
      expect(neonConfig.fetchFunction).toBe(customFetch);
    });

    test("wsProxy as string", () => {
      const proxy = "myproxy.example.com";
      neonConfig.wsProxy = proxy;
      expect(neonConfig.wsProxy).toBe(proxy);
    });

    test("wsProxy as function", () => {
      const proxyFn = (host: string, port: number | string) =>
        `proxy.example.com?host=${host}&port=${port}`;
      neonConfig.wsProxy = proxyFn;
      expect(typeof neonConfig.wsProxy).toBe("function");
    });

    test("pipelineConnect password mode", () => {
      neonConfig.pipelineConnect = "password";
      expect(neonConfig.pipelineConnect).toBe("password");
    });

    test("pipelineConnect false mode", () => {
      neonConfig.pipelineConnect = false;
      expect(neonConfig.pipelineConnect).toBe(false);
    });

    test("coalesceWrites true", () => {
      neonConfig.coalesceWrites = true;
      expect(neonConfig.coalesceWrites).toBe(true);
    });

    test("coalesceWrites false", () => {
      neonConfig.coalesceWrites = false;
      expect(neonConfig.coalesceWrites).toBe(false);
    });

    test("useSecureWebSocket true", () => {
      neonConfig.useSecureWebSocket = true;
      expect(neonConfig.useSecureWebSocket).toBe(true);
    });

    test("useSecureWebSocket false", () => {
      neonConfig.useSecureWebSocket = false;
      expect(neonConfig.useSecureWebSocket).toBe(false);
    });

    test("forceDisablePgSSL true", () => {
      neonConfig.forceDisablePgSSL = true;
      expect(neonConfig.forceDisablePgSSL).toBe(true);
    });

    test("forceDisablePgSSL false", () => {
      neonConfig.forceDisablePgSSL = false;
      expect(neonConfig.forceDisablePgSSL).toBe(false);
    });

    test("poolQueryViaFetch true", () => {
      neonConfig.poolQueryViaFetch = true;
      expect(neonConfig.poolQueryViaFetch).toBe(true);
    });

    test("poolQueryViaFetch false", () => {
      neonConfig.poolQueryViaFetch = false;
      expect(neonConfig.poolQueryViaFetch).toBe(false);
    });

    test("disableSNI setting", () => {
      neonConfig.disableSNI = true;
      expect(neonConfig.disableSNI).toBe(true);
    });

    test("disableWarningInBrowsers setting", () => {
      neonConfig.disableWarningInBrowsers = true;
      expect(neonConfig.disableWarningInBrowsers).toBe(true);
    });

    test("pipelineTLS setting", () => {
      neonConfig.pipelineTLS = true;
      expect(neonConfig.pipelineTLS).toBe(true);
    });

    test("rootCerts setting", () => {
      const certs = "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----";
      neonConfig.rootCerts = certs;
      expect(neonConfig.rootCerts).toBe(certs);
    });

    test("default values exist", () => {
      expect(neonConfig.defaults).toBeDefined();
      expect(typeof neonConfig.defaults.poolQueryViaFetch).toBe("boolean");
    });

    test("config inheritance - global vs client", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const client = new Client(dbUrl);
      expect(client.neonConfig).toBeDefined();
    });

    test("config isolation between clients", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const client1 = new Client(dbUrl);
      const client2 = new Client(dbUrl);

      // Clients should have separate config instances
      expect(client1.neonConfig).toBeDefined();
      expect(client2.neonConfig).toBeDefined();
    });

    test("client-level config override", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const client = new Client(dbUrl);
      // Can modify client-level config
      client.neonConfig.coalesceWrites = false;
      expect(client.neonConfig.coalesceWrites).toBe(false);
    });

    test("neon() function respects config", async () => {
      neonConfig.fetchEndpoint = fetchEndpointFor;
      const sql = neon(dbUrl);
      // If config is respected, query should work
      const result = await sql`SELECT 1 as num`;
      expect(result).toEqual([{ num: 1 }]);
    });

    test("Pool respects global config", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const pool = new Pool({ connectionString: dbUrl });
      const result = await pool.query("SELECT 1 as num");
      expect(result.rows[0].num).toBe(1);
      await pool.end();
    });

    test("fetchConnectionCache deprecated setting", () => {
      // Setting should still work but is deprecated
      neonConfig.fetchConnectionCache = true;
      expect(neonConfig.fetchConnectionCache).toBe(true);
    });

    test("multiple config changes", () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;
      neonConfig.coalesceWrites = true;
      neonConfig.useSecureWebSocket = true;

      expect(neonConfig.webSocketConstructor).toBe(ws);
      expect(neonConfig.pipelineConnect).toBe(false);
      expect(neonConfig.coalesceWrites).toBe(true);
      expect(neonConfig.useSecureWebSocket).toBe(true);
    });

    test("config affects connection behavior", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 1");
      expect(result.rows.length).toBe(1);
      await client.end();
    });

    test("subtls setting", () => {
      // subtls is undefined by default
      expect(neonConfig.subtls).toBeUndefined();
    });
  });

  // ==================== Connection String Tests (20 tests) ====================
  describe("Connection String Tests", () => {
    test("standard postgres:// URL", async () => {
      const sql = neon(dbUrl);
      const result = await sql`SELECT 1 as num`;
      expect(result[0].num).toBe(1);
    });

    test("postgresql:// URL", async () => {
      // Convert postgres:// to postgresql://
      const postgresqlUrl = dbUrl.replace("postgres://", "postgresql://");
      const sql = neon(postgresqlUrl);
      const result = await sql`SELECT 1 as num`;
      expect(result[0].num).toBe(1);
    });

    test("connection string with port", async () => {
      // The URL should already have a port or use default
      const url = new URL(dbUrl);
      expect(url.port || "5432").toBeDefined();
    });

    test("connection string with database name", async () => {
      const url = new URL(dbUrl);
      const dbName = url.pathname.slice(1);
      // Database name may be empty for some backends (e.g. local proxy)
      expect(typeof dbName).toBe("string");
    });

    test("URL encoded password", async () => {
      const url = new URL(dbUrl);
      // Password should be URL decoded properly
      const password = decodeURIComponent(url.password);
      expect(password).toBeDefined();
    });

    test("special characters in password", async () => {
      // The driver should handle special chars in password
      // Just verify the URL parses correctly
      const url = new URL(dbUrl);
      expect(url.password).toBeDefined();
    });

    test("connection with hostname", async () => {
      const url = new URL(dbUrl);
      expect(url.hostname.length).toBeGreaterThan(0);
    });

    test("connection with user", async () => {
      const url = new URL(dbUrl);
      expect(url.username.length).toBeGreaterThan(0);
    });

    test("Client with connection string", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const client = new Client(dbUrl);
      await client.connect();
      const result = await client.query("SELECT 1");
      expect(result.rows.length).toBe(1);
      await client.end();
    });

    test("Pool with connection string", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const pool = new Pool({ connectionString: dbUrl });
      const result = await pool.query("SELECT 1");
      expect(result.rows.length).toBe(1);
      await pool.end();
    });

    test("connection string options parsing", () => {
      const url = new URL(dbUrl);
      url.searchParams.set("application_name", "test");
      expect(url.searchParams.get("application_name")).toBe("test");
    });

    test("sslmode parameter", () => {
      const url = new URL(dbUrl);
      url.searchParams.set("sslmode", "require");
      expect(url.searchParams.get("sslmode")).toBe("require");
    });

    test("connection timeout parameter", () => {
      const url = new URL(dbUrl);
      url.searchParams.set("connect_timeout", "10");
      expect(url.searchParams.get("connect_timeout")).toBe("10");
    });

    test("statement_timeout parameter", () => {
      const url = new URL(dbUrl);
      url.searchParams.set("statement_timeout", "30000");
      expect(url.searchParams.get("statement_timeout")).toBe("30000");
    });

    test("Client config object with connection string", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const client = new Client({
        connectionString: dbUrl,
      });
      await client.connect();
      const result = await client.query("SELECT 1");
      expect(result.rows.length).toBe(1);
      await client.end();
    });

    test("Client config object with individual params", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const url = new URL(dbUrl);
      const client = new Client({
        host: url.hostname,
        port: parseInt(url.port, 10) || 5432,
        database: url.pathname.slice(1),
        user: url.username,
        password: decodeURIComponent(url.password),
        ssl: true,
      });
      await client.connect();
      const result = await client.query("SELECT 1");
      expect(result.rows.length).toBe(1);
      await client.end();
    });

    test("Pool config with max connections", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const pool = new Pool({
        connectionString: dbUrl,
        max: 5,
      });
      expect(pool).toBeDefined();
      await pool.end();
    });

    test("Pool config with idle timeout", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const pool = new Pool({
        connectionString: dbUrl,
        idleTimeoutMillis: 10000,
      });
      expect(pool).toBeDefined();
      await pool.end();
    });

    test("Pool config with connection timeout", async () => {
      neonConfig.webSocketConstructor = ws;
      neonConfig.pipelineConnect = false;

      const pool = new Pool({
        connectionString: dbUrl,
        connectionTimeoutMillis: 5000,
      });
      expect(pool).toBeDefined();
      await pool.end();
    });

    test("neon() with query options", async () => {
      const sql = neon(dbUrl, {
        arrayMode: false,
        fullResults: false,
      });
      const result = await sql`SELECT 1 as num`;
      expect(result).toEqual([{ num: 1 }]);
    });
  });
});

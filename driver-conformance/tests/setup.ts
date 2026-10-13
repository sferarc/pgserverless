/**
 * Global test setup for the pgserverless driver conformance suite
 *
 * This file configures the test environment by importing helpers/connection,
 * which sets up neonConfig globally. Tests should pass against a pgserverless
 * server and against a Neon database.
 *
 * neonConfig is configured via environment variables:
 *   - PIPELINE_CONNECT: set to "false" to disable pipelineConnect (default: "password")
 *   - PORT: server port, overriding the port in DATABASE_URL (default: 443, or 80 without TLS)
 *   - PGSERVERLESS_TLS: set to "false" to use http and ws (default: true)
 */

// Re-export from connection module (also triggers neonConfig setup)
export { getDatabaseUrl } from "../helpers/connection";

// Environment variable (for reference)
export const DATABASE_URL = process.env.DATABASE_URL;

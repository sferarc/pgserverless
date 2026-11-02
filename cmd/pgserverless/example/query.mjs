// Queries a database through pgserverless over both transports, using the
// unmodified @neondatabase/serverless client.
//
// The point of this script is that nothing in it is special. The client is the
// published one, the only configuration is where to send requests, and both
// transports are exercised, because they take different paths through the
// server and it is possible for one to work while the other does not.

import { neon, neonConfig, Pool } from '@neondatabase/serverless'
import ws from 'ws'

const base = process.env.PGSERVERLESS_URL ?? 'http://localhost:4000'
const dsn = process.env.DATABASE_URL ?? 'postgres://app:pw@localhost/app'
// URL.host already carries the port when there is one, so it is used whole.
// Reassembling it from hostname and port is where this went wrong first.
const { host } = new URL(base)

// HTTP transport. One request, one response, no connection kept open.
neonConfig.fetchEndpoint = `${base}/sql`

// WebSocket transport, used by Pool and Client.
neonConfig.wsProxy = `${host}/v2`
neonConfig.useSecureWebSocket = base.startsWith('https')
neonConfig.webSocketConstructor = ws

// The client pipelines its first three messages by default, assuming the server
// will ask for a cleartext password. PostgreSQL asks for SCRAM instead, so a
// client left on the default hangs rather than failing. This is a property of
// the protocol, not of this server, and the specification records it.
neonConfig.pipelineConnect = false

async function main() {
  const sql = neon(dsn)

  const [{ version }] = await sql`SELECT version()`
  console.log('http      :', version.split(',')[0])

  // int8 arrives as a string. A JSON number is a float64 and would lose the
  // low bits of anything past 2^53, so the wire format keeps it exact and the
  // client parses it. Printing the type is the whole demonstration.
  const [big] = await sql`SELECT 9223372036854775807::int8 AS n`
  console.log('http int8 :', big.n, `(${typeof big.n})`)

  const pool = new Pool({ connectionString: dsn })
  try {
    const { rows } = await pool.query('SELECT now() AS at')
    console.log('websocket :', rows[0].at.toISOString())
  } finally {
    await pool.end()
  }

  console.log('\nboth transports answered.')
}

main().catch((err) => {
  console.error('failed:', err.message)
  process.exit(1)
})

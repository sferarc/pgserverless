/**
 * Test data fixtures and generators
 *
 * Provides consistent test data for deterministic testing
 */

/**
 * Sample string values for testing
 */
export const strings = {
  empty: "",
  simple: "hello",
  withSpaces: "hello world",
  unicode: "Hello \u4e16\u754c \ud83c\udf0d",
  specialChars: "O'Reilly & Associates",
  sqlInjection: "'; DROP TABLE users; --",
  newlines: "line1\nline2\nline3",
  tabs: "col1\tcol2\tcol3",
  quotes: "\"double\" and 'single'",
  backslash: "path\\to\\file",
  null: "\x00hidden",
  long: "x".repeat(10000),
  veryLong: "x".repeat(100000),
};

/**
 * Sample number values for testing
 */
export const numbers = {
  zero: 0,
  one: 1,
  negative: -42,
  float: Math.PI,
  scientific: 1.23e10,
  maxSafeInt: Number.MAX_SAFE_INTEGER,
  minSafeInt: Number.MIN_SAFE_INTEGER,
  maxValue: Number.MAX_VALUE,
  minValue: Number.MIN_VALUE,
  infinity: Infinity,
  negInfinity: -Infinity,
  nan: NaN,
  epsilon: Number.EPSILON,
};

/**
 * Sample bigint values for testing
 */
export const bigints = {
  zero: 0n,
  small: 42n,
  large: 9223372036854775807n,
  negative: -9223372036854775808n,
};

/**
 * Sample boolean values
 */
export const booleans = {
  true: true,
  false: false,
};

/**
 * Sample date values for testing
 */
export const dates = {
  epoch: new Date(0),
  y2k: new Date("2000-01-01T00:00:00.000Z"),
  now: new Date("2024-01-15T12:30:45.123Z"), // Fixed date for determinism
  future: new Date("2099-12-31T23:59:59.999Z"),
  minDate: new Date(-8640000000000000),
  maxDate: new Date(8640000000000000),
};

/**
 * Sample binary data for testing
 */
export const binary = {
  empty: new Uint8Array([]),
  simple: new Uint8Array([0x01, 0x02, 0x03, 0x04]),
  zeros: new Uint8Array(100),
  random: new Uint8Array([0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe]),
  large: new Uint8Array(10000).fill(0xab),
  allBytes: new Uint8Array(256).map((_, i) => i),
};

/**
 * Sample JSON values for testing
 */
export const json = {
  null: null,
  boolean: true,
  number: 42,
  string: "hello",
  array: [1, 2, 3],
  object: { key: "value" },
  nested: {
    level1: {
      level2: {
        level3: {
          value: "deep",
        },
      },
    },
  },
  mixed: {
    string: "hello",
    number: 42,
    boolean: true,
    null: null,
    array: [1, "two", false],
    object: { nested: true },
  },
  unicode: { emoji: "\ud83d\ude00", chinese: "\u4e2d\u6587" },
  empty: {},
  emptyArray: [],
};

/**
 * Sample array values for testing PostgreSQL arrays
 */
export const arrays = {
  emptyInt: [] as number[],
  intArray: [1, 2, 3, 4, 5],
  textArray: ["a", "b", "c"],
  mixedNulls: [1, null, 3, null, 5],
  nested: [
    [1, 2],
    [3, 4],
    [5, 6],
  ],
  unicode: ["\u4e2d\u6587", "\ud83d\ude00", "hello"],
  withQuotes: ["it's", '"quoted"', "normal"],
};

/**
 * Sample UUID values for testing
 */
export const uuids = {
  nil: "00000000-0000-0000-0000-000000000000",
  v4: "550e8400-e29b-41d4-a716-446655440000",
  max: "ffffffff-ffff-ffff-ffff-ffffffffffff",
};

/**
 * Sample network values for testing
 */
export const network = {
  ipv4: "192.168.1.1",
  ipv6: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
  ipv6Short: "::1",
  cidr: "192.168.1.0/24",
  macaddr: "08:00:2b:01:02:03",
};

/**
 * Sample geometric values for testing
 */
export const geometric = {
  point: "(1.5, 2.5)",
  line: "{1, 2, 3}",
  lseg: "[(0,0), (1,1)]",
  box: "(1,1), (0,0)",
  path: "[(0,0), (1,1), (2,0)]",
  polygon: "((0,0), (1,1), (1,0))",
  circle: "<(0,0), 1>",
};

/**
 * Generate a test table schema
 */
export function generateTestTableSchema(name: string = "test_table"): {
  createSQL: string;
  dropSQL: string;
  columns: string[];
} {
  return {
    createSQL: `
      CREATE TABLE IF NOT EXISTS ${name} (
        id SERIAL PRIMARY KEY,
        text_col TEXT,
        int_col INTEGER,
        bool_col BOOLEAN,
        float_col DOUBLE PRECISION,
        json_col JSONB,
        timestamp_col TIMESTAMPTZ DEFAULT NOW(),
        uuid_col UUID
      )
    `,
    dropSQL: `DROP TABLE IF EXISTS ${name}`,
    columns: [
      "id",
      "text_col",
      "int_col",
      "bool_col",
      "float_col",
      "json_col",
      "timestamp_col",
      "uuid_col",
    ],
  };
}

/**
 * Generate sample rows for insertion
 */
export function generateSampleRows(count: number = 10): Array<{
  text_col: string;
  int_col: number;
  bool_col: boolean;
  float_col: number;
  json_col: object;
}> {
  return Array.from({ length: count }, (_, i) => ({
    text_col: `row_${i + 1}`,
    int_col: i + 1,
    bool_col: i % 2 === 0,
    float_col: (i + 1) * 1.5,
    json_col: { index: i + 1, label: `item_${i + 1}` },
  }));
}

/**
 * PostgreSQL type OIDs for common types
 */
export const pgTypeOids = {
  bool: 16,
  bytea: 17,
  char: 18,
  int8: 20,
  int2: 21,
  int4: 23,
  text: 25,
  oid: 26,
  json: 114,
  xml: 142,
  float4: 700,
  float8: 701,
  money: 790,
  macaddr: 829,
  inet: 869,
  cidr: 650,
  bpchar: 1042,
  varchar: 1043,
  date: 1082,
  time: 1083,
  timestamp: 1114,
  timestamptz: 1184,
  interval: 1186,
  timetz: 1266,
  bit: 1560,
  varbit: 1562,
  numeric: 1700,
  uuid: 2950,
  jsonb: 3802,
  int4range: 3904,
  int8range: 3926,
  numrange: 3906,
  tsrange: 3908,
  tstzrange: 3910,
  daterange: 3912,
};

/**
 * PostgreSQL error codes for common errors
 */
export const pgErrorCodes = {
  // Class 00 — Successful Completion
  successfulCompletion: "00000",

  // Class 22 — Data Exception
  divisionByZero: "22012",
  invalidTextRepresentation: "22P02",
  numericValueOutOfRange: "22003",
  stringDataRightTruncation: "22001",

  // Class 23 — Integrity Constraint Violation
  notNullViolation: "23502",
  foreignKeyViolation: "23503",
  uniqueViolation: "23505",
  checkViolation: "23514",
  exclusionViolation: "23P01",

  // Class 40 — Transaction Rollback
  transactionRollback: "40000",
  serializationFailure: "40001",
  deadlockDetected: "40P01",

  // Class 42 — Syntax Error or Access Rule Violation
  syntaxError: "42601",
  undefinedColumn: "42703",
  undefinedTable: "42P01",
  duplicateColumn: "42701",
  duplicateTable: "42P07",
  datatypeMismatch: "42804",
  insufficientPrivilege: "42501",
  undefinedFunction: "42883",
  undefinedObject: "42704",

  // Class 53 — Insufficient Resources
  outOfMemory: "53200",
  diskFull: "53100",

  // Class 57 — Operator Intervention
  queryCanceled: "57014",
  adminShutdown: "57P01",

  // Class 08 — Connection Exception
  connectionException: "08000",
  connectionDoesNotExist: "08003",
  connectionFailure: "08006",

  // Class 28 — Invalid Authorization Specification
  invalidAuthorizationSpec: "28000",
  invalidPassword: "28P01",
};

/**
 * Generate a random but deterministic string based on seed
 */
export function deterministicString(seed: number, length: number): string {
  const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let result = "";
  let state = seed;
  for (let i = 0; i < length; i++) {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    result += chars[state % chars.length];
  }
  return result;
}

/**
 * Generate a unique table name for tests
 */
export function uniqueTableName(prefix: string = "test"): string {
  const timestamp = Date.now();
  const random = Math.floor(Math.random() * 10000);
  return `${prefix}_${timestamp}_${random}`;
}

// Runs once, when MongoDB starts with an empty data volume (as the root user).
// Creates least-privilege users; passwords come from the container environment.

function requireEnv(name) {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is not set`);
  }
  return value;
}

// Bot: read/write only its own database.
db.getSiblingDB("polymarket").createUser({
  user: requireEnv("MONGO_APP_USER"),
  pwd: requireEnv("MONGO_APP_PASSWORD"),
  roles: [{ role: "readWrite", db: "polymarket" }],
});

// Tests: read/write and drop only the throwaway test database.
db.getSiblingDB("polymarket_test").createUser({
  user: requireEnv("MONGO_TEST_USER"),
  pwd: requireEnv("MONGO_TEST_PASSWORD"),
  roles: [{ role: "dbOwner", db: "polymarket_test" }],
});

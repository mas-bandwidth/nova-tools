CREATE TABLE messages (
  id INTEGER PRIMARY KEY,
  role TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  tokens_in INTEGER,
  tokens_out INTEGER,
  cache_write INTEGER,
  cache_read INTEGER,
  reasoning INTEGER,
  usd REAL
);
INSERT INTO messages (role, provider, model, tokens_in, tokens_out, cache_write, cache_read, reasoning, usd) VALUES
  ('assistant', 'deepseek', 'deepseek-chat', 100, 50, 10, 20, 5, 0.25),
  ('assistant', 'deepseek', 'deepseek-chat', 7, 3, NULL, NULL, NULL, 0.50),
  ('assistant', 'deepseek', 'deepseek-chat', 1, 2, NULL, NULL, NULL, 0.25);

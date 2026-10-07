CREATE TABLE message (
  id INTEGER PRIMARY KEY,
  data TEXT NOT NULL,
  time_created INTEGER NOT NULL
);
INSERT INTO message (data, time_created) VALUES
  ('{"role":"assistant","providerID":"deepseek","modelID":"deepseek-chat","tokens":{"input":100,"output":50,"cache":{"write":10,"read":20},"reasoning":5},"cost":0.25}', 1760000000000),
  ('{"role":"assistant","providerID":"deepseek","modelID":"deepseek-chat","tokens":{"input":7,"output":3},"cost":0.50}', 1760000000000),
  ('{"role":"assistant","providerID":"deepseek","modelID":"deepseek-chat","tokens":{"input":1,"output":2},"cost":0.25}', 1760000000000);

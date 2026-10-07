-- Billing for friends: api or subscription
ALTER TABLE friends ADD COLUMN billing text DEFAULT 'subscription';

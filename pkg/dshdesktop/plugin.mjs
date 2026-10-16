// Native contract: dsh-agent/dsh-llm in Harness 0.2.0-rc.2, source 5e9e301dd9dc8923b2762f76dacfc5751f6ca851.
import { applyReceiver } from './nova-hook-dsh-receiver.mjs';
export const name = 'nova-hook';
export const inject = ['agents'];
export function apply(ctx, config) { const receiver = applyReceiver(ctx, config); return receiver.dispose; }

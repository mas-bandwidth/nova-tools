import assert from 'node:assert/strict'

globalThis.fetch = async () => {
  throw new Error('global fetch must never run in this probe')
}

const { createOpenAICompatible } = await import('@ai-sdk/openai-compatible')
const { createOpenAI } = await import('@ai-sdk/openai')

const prompt = [{ role: 'user', content: [{ type: 'text', text: 'fixture prompt' }] }]
const call = { prompt, headers: { 'x-opencode-session': 'fixture-session-001', 'User-Agent': 'nova-fixture/1.0' } }
const seen = []
const fakeFetch = async (url, init = {}) => {
  const headers = Object.fromEntries(new Headers(init.headers).entries())
  seen.push({ url: String(url), method: init.method, headers })
  if (String(url).endsWith('/responses')) {
    return new Response(JSON.stringify({
      id: 'resp_fixture', object: 'response', created_at: 0, status: 'completed',
      model: 'gpt-5.6-terra', output: [{ type: 'message', id: 'msg_fixture', status: 'completed', role: 'assistant', content: [{ type: 'output_text', text: 'ok', annotations: [] }] }],
      usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 },
    }), { status: 200, headers: { 'content-type': 'application/json' } })
  }
  return new Response(JSON.stringify({
    id: 'chatcmpl_fixture', object: 'chat.completion', created: 0, model: 'deepseek-v4-flash',
    choices: [{ index: 0, message: { role: 'assistant', content: 'ok' }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
  }), { status: 200, headers: { 'content-type': 'application/json' } })
}

async function chat(name, baseURL, expected) {
  const provider = createOpenAICompatible({ name, baseURL, apiKey: 'fixture-only-key', fetch: fakeFetch })
  await provider.languageModel('deepseek-v4-flash').doGenerate(call)
  const request = seen.at(-1)
  assert.equal(request.url, expected, `${name} URL`)
  assert.equal(request.method, 'POST', `${name} method`)
  assert.equal(request.headers['x-opencode-session'], 'fixture-session-001', `${name} session`)
  assert.ok(request.headers['user-agent'].startsWith('nova-fixture/1.0 '), `${name} user-agent prefix`)
}

await chat('go', 'https://opencode.ai/zen/go/v1', 'https://opencode.ai/zen/go/v1/chat/completions')
await chat('zen', 'https://opencode.ai/zen/v1', 'https://opencode.ai/zen/v1/chat/completions')

const responses = createOpenAI({ name: 'zen-responses', baseURL: 'https://opencode.ai/zen/v1', apiKey: 'fixture-only-key', fetch: fakeFetch })
await responses.languageModel('gpt-5.6-terra').doGenerate(call)
const responseRequest = seen.at(-1)
assert.equal(responseRequest.url, 'https://opencode.ai/zen/v1/responses', 'responses URL')
assert.equal(responseRequest.headers['x-opencode-session'], 'fixture-session-001', 'responses session')
assert.ok(responseRequest.headers['user-agent'].startsWith('nova-fixture/1.0 '), 'responses user-agent prefix')

const terminalBase = createOpenAICompatible({ name: 'negative', baseURL: 'https://opencode.ai/zen/v1/chat/completions', apiKey: 'fixture-only-key', fetch: fakeFetch })
await terminalBase.languageModel('deepseek-v4-flash').doGenerate(call)
const negative = seen.at(-1)
assert.equal(negative.url, 'https://opencode.ai/zen/v1/chat/completions/chat/completions', 'terminal base duplicates path')

assert.equal(seen.length, 4, 'exactly four intercepted requests')
assert.ok(seen.every(request => request.method === 'POST'), 'all requests use POST')

console.log(JSON.stringify({ requests: seen.map(({ url, headers }) => ({ url, session: headers['x-opencode-session'], userAgent: headers['user-agent'] })), pass: true }, null, 2))

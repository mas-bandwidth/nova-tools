import assert from 'node:assert/strict'

globalThis.fetch = async () => {
  throw new Error('global fetch must never run in this probe')
}

const { createOpenAICompatible } = await import('@ai-sdk/openai-compatible')
const { createOpenAI } = await import('@ai-sdk/openai')

const encoder = new TextEncoder()
const prompt = [{ role: 'user', content: [{ type: 'text', text: 'fixture streaming prompt' }] }]
const tool = {
  type: 'function',
  name: 'fixture_tool',
  description: 'fixture only',
  inputSchema: {
    type: 'object',
    properties: { value: { type: 'string' } },
    required: ['value'],
    additionalProperties: false,
  },
}
const call = {
  prompt,
  tools: [tool],
  headers: {
    'x-opencode-session': 'fixture-stream-session-001',
    'User-Agent': 'nova-stream-fixture/1.0',
  },
}

function sse(events) {
  return new ReadableStream({
    start(controller) {
      for (const event of events) controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`))
      controller.enqueue(encoder.encode('data: [DONE]\n\n'))
      controller.close()
    },
  })
}

function chatEvents(model, includeUsage = true) {
  return [
    {
      id: 'chat_fixture', object: 'chat.completion.chunk', created: 0, model,
      choices: [{ index: 0, delta: { tool_calls: [{ index: 0, id: 'call_fixture', type: 'function', function: { name: 'fixture_tool', arguments: '{"value":' } }] }, finish_reason: null }],
    },
    {
      id: 'chat_fixture', object: 'chat.completion.chunk', created: 0, model,
      choices: [{ index: 0, delta: { tool_calls: [{ index: 0, function: { arguments: '"ok"}' } }] }, finish_reason: null }],
    },
    {
      id: 'chat_fixture', object: 'chat.completion.chunk', created: 0, model,
      choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
      ...(includeUsage ? { usage: { prompt_tokens: 11, completion_tokens: 7, total_tokens: 18, prompt_tokens_details: { cached_tokens: 3 }, completion_tokens_details: { reasoning_tokens: 2 } } } : {}),
    },
  ]
}

function responsesEvents(model) {
  return [
    { type: 'response.created', response: { id: 'resp_fixture', object: 'response', created_at: 0, status: 'in_progress', model } },
    { type: 'response.output_item.added', output_index: 0, item: { id: 'fc_fixture', type: 'function_call', status: 'in_progress', call_id: 'call_fixture', name: 'fixture_tool', arguments: '' } },
    { type: 'response.function_call_arguments.delta', output_index: 0, item_id: 'fc_fixture', delta: '{"value":' },
    { type: 'response.function_call_arguments.delta', output_index: 0, item_id: 'fc_fixture', delta: '"ok"}' },
    { type: 'response.function_call_arguments.done', output_index: 0, item_id: 'fc_fixture', name: 'fixture_tool', arguments: '{"value":"ok"}' },
    { type: 'response.output_item.done', output_index: 0, item: { id: 'fc_fixture', type: 'function_call', status: 'completed', call_id: 'call_fixture', name: 'fixture_tool', arguments: '{"value":"ok"}' } },
    { type: 'response.completed', response: { id: 'resp_fixture', object: 'response', created_at: 0, status: 'completed', model, output: [], usage: { input_tokens: 11, output_tokens: 7, total_tokens: 18, input_tokens_details: { cached_tokens: 3 }, output_tokens_details: { reasoning_tokens: 2 } } } },
  ]
}

const seen = []
function fakeFetch(kind, events) {
  return async (url, init = {}) => {
    const headers = Object.fromEntries(new Headers(init.headers).entries())
    const body = JSON.parse(String(init.body))
    seen.push({ kind, url: String(url), method: init.method, headers, body })
    return new Response(sse(events), { status: 200, headers: { 'content-type': 'text/event-stream' } })
  }
}

async function consume(model) {
  const result = await model.doStream(call)
  const events = []
  for await (const event of result.stream) events.push(event)
  return events
}

function verifyRequest(request, expectedURL, expectedModel, protocol) {
  assert.equal(request.url, expectedURL)
  assert.equal(request.method, 'POST')
  assert.equal(request.headers['x-opencode-session'], 'fixture-stream-session-001')
  assert.ok(request.headers['user-agent'].startsWith('nova-stream-fixture/1.0 '))
  assert.equal(request.body.model, expectedModel)
  assert.equal(request.body.stream, true)
  assert.equal(request.body.tools.length, 1)
  assert.equal(request.body.tools[0].type, 'function')
  if (protocol === 'chat') {
    assert.equal(request.body.stream_options.include_usage, true, 'chat requests stream usage')
    assert.equal(request.body.tools[0].function.name, 'fixture_tool')
    assert.deepEqual(request.body.tools[0].function.parameters, tool.inputSchema)
    return
  }
  assert.equal(protocol, 'responses')
  assert.equal(request.body.tools[0].name, 'fixture_tool')
  assert.deepEqual(request.body.tools[0].parameters, tool.inputSchema)
}

function verifyToolAndUsage(events, label) {
  const calls = events.filter((event) => event.type === 'tool-call')
  assert.equal(calls.length, 1, `${label} one tool call`)
  assert.equal(calls[0].toolCallId, 'call_fixture', `${label} tool id`)
  assert.equal(calls[0].toolName, 'fixture_tool', `${label} tool name`)
  assert.equal(calls[0].input, '{"value":"ok"}', `${label} tool arguments`)
  assert.equal(events.filter(event => event.type === 'error').length, 0, `${label} no stream errors`)
  const finishes = events.filter(event => event.type === 'finish')
  assert.equal(finishes.length, 1, `${label} one finish event`)
  const finish = finishes[0]
  assert.equal(finish.finishReason.unified, 'tool-calls', `${label} tool-call finish`)
  assert.equal(finish.usage.inputTokens.total, 11, `${label} input usage`)
  assert.equal(finish.usage.outputTokens.total, 7, `${label} output usage`)
  assert.equal(finish.usage.inputTokens.cacheRead, 3, `${label} cache usage`)
  assert.equal(finish.usage.outputTokens.reasoning, 2, `${label} reasoning usage`)
  assert.equal(finish.usage.inputTokens.total + finish.usage.outputTokens.total, 18, `${label} inclusive total; do not add cache or reasoning twice`)
}

const goProvider = createOpenAICompatible({ name: 'go', baseURL: 'https://opencode.ai/zen/go/v1', apiKey: 'fixture-only-key', includeUsage: true, fetch: fakeFetch('go', chatEvents('deepseek-v4-flash')) })
const goEvents = await consume(goProvider.languageModel('deepseek-v4-flash'))
verifyRequest(seen.at(-1), 'https://opencode.ai/zen/go/v1/chat/completions', 'deepseek-v4-flash', 'chat')
verifyToolAndUsage(goEvents, 'go')

const zenProvider = createOpenAICompatible({ name: 'zen', baseURL: 'https://opencode.ai/zen/v1', apiKey: 'fixture-only-key', includeUsage: true, fetch: fakeFetch('zen', chatEvents('deepseek-v4-flash')) })
const zenEvents = await consume(zenProvider.languageModel('deepseek-v4-flash'))
verifyRequest(seen.at(-1), 'https://opencode.ai/zen/v1/chat/completions', 'deepseek-v4-flash', 'chat')
verifyToolAndUsage(zenEvents, 'zen')

const unknownUsage = createOpenAICompatible({ name: 'unknown-usage', baseURL: 'https://opencode.ai/zen/v1', apiKey: 'fixture-only-key', includeUsage: true, fetch: fakeFetch('unknown-usage', chatEvents('deepseek-v4-flash', false)) })
const unknownUsageEvents = await consume(unknownUsage.languageModel('deepseek-v4-flash'))
const unknownUsageFinish = unknownUsageEvents.find((event) => event.type === 'finish')
assert.ok(unknownUsageFinish, 'unknown usage finish')
assert.equal(unknownUsageFinish.usage.inputTokens.total, undefined, 'missing input usage remains unknown')
assert.equal(unknownUsageFinish.usage.inputTokens.cacheRead, undefined, 'missing cache usage remains unknown')
assert.equal(unknownUsageFinish.usage.outputTokens.total, undefined, 'missing output usage remains unknown')
assert.equal(unknownUsageFinish.usage.outputTokens.reasoning, undefined, 'missing reasoning usage remains unknown')

verifyRequest(seen.at(-1), 'https://opencode.ai/zen/v1/chat/completions', 'deepseek-v4-flash', 'chat')

const responseProvider = createOpenAI({ name: 'zen-responses', baseURL: 'https://opencode.ai/zen/v1', apiKey: 'fixture-only-key', fetch: fakeFetch('responses', responsesEvents('gpt-5.6-terra')) })
const responseEvents = await consume(responseProvider.languageModel('gpt-5.6-terra'))
verifyRequest(seen.at(-1), 'https://opencode.ai/zen/v1/responses', 'gpt-5.6-terra', 'responses')
verifyToolAndUsage(responseEvents, 'responses')

const malformed = createOpenAICompatible({
  name: 'malformed', baseURL: 'https://opencode.ai/zen/v1', apiKey: 'fixture-only-key', includeUsage: true,
  fetch: fakeFetch('malformed', [{ not: 'a-chat-completion-chunk' }]),
})
const malformedEvents = await consume(malformed.languageModel('deepseek-v4-flash'))
assert.ok(malformedEvents.some((event) => event.type === 'error'), 'malformed SSE must emit an error')
const malformedFinish = malformedEvents.find((event) => event.type === 'finish')
assert.ok(!malformedFinish || malformedFinish.finishReason.unified === 'error', 'malformed SSE must not finish successfully')

assert.equal(seen.length, 5, 'exactly five intercepted requests')
assert.ok(seen.every((request) => request.method === 'POST'), 'all intercepted requests use POST')
console.log(JSON.stringify({
  pass: true,
  limits: 'synthetic intercepted SDK stream only; no billing, provider, native-admission, or compatibility proof',
  streams: seen.map(({ kind, url }) => ({ kind, url })),
  eventTypes: { go: goEvents.map((event) => event.type), zen: zenEvents.map((event) => event.type), unknownUsage: unknownUsageEvents.map((event) => event.type), responses: responseEvents.map((event) => event.type), malformed: malformedEvents.map((event) => event.type) },
}, null, 2))

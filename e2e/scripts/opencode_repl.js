#!/usr/bin/env node
/**
 * Minimal OpenAI REPL used by the opencode e2e provider test.
 * Emits "❯" as the prompt indicator so the bridge test suite can detect
 * readiness and completion after each turn.
 */
'use strict';

const https = require('https');
const readline = require('readline');

const API_KEY = process.env.OPENAI_API_KEY || '';
const MODEL = process.env.OPENCODE_MODEL || 'gpt-4o-mini';

const history = [];

function callOpenAI(messages) {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify({ model: MODEL, messages });
    const options = {
      hostname: 'api.openai.com',
      port: 443,
      path: '/v1/chat/completions',
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${API_KEY}`,
        'Content-Length': Buffer.byteLength(body),
      },
    };
    const req = https.request(options, (res) => {
      let data = '';
      res.on('data', (chunk) => { data += chunk; });
      res.on('end', () => {
        try {
          const parsed = JSON.parse(data);
          if (parsed.error) {
            reject(new Error(parsed.error.message));
          } else if (!parsed.choices || !parsed.choices[0] || !parsed.choices[0].message) {
            reject(new Error(`unexpected API response shape: ${data}`));
          } else {
            resolve(parsed.choices[0].message.content);
          }
        } catch (e) {
          reject(e);
        }
      });
    });
    req.on('error', reject);
    req.write(body);
    req.end();
  });
}

async function main() {
  if (!API_KEY) {
    process.stderr.write('fatal: OPENAI_API_KEY is not set\n');
    process.exit(1);
  }

  process.stdout.write('❯\n');

  const rl = readline.createInterface({
    input: process.stdin,
    output: null,
    terminal: false,
    crlfDelay: Infinity,
  });

  for await (const line of rl) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    history.push({ role: 'user', content: trimmed });

    try {
      const response = await callOpenAI(history);
      history.push({ role: 'assistant', content: response });
      process.stdout.write(response + '\n\n❯\n');
    } catch (e) {
      process.stderr.write(`opencode_repl error: ${e.message}\n`);
      process.stdout.write('\n❯\n');
    }
  }
}

main().catch((e) => {
  process.stderr.write(`fatal: ${e.message}\n`);
  process.exit(1);
});

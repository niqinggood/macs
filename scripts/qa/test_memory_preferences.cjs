'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const context = { window: {} };
vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../web/memory_groups.js'), 'utf8'), context);
const { preferenceArchive, preferenceStatement, preferenceConversations, conversationMessages, conversationUserMessages } = context.window.MacsMemoryGroups;
function record(id, subject, value, options = {}) {
  return {
    id,
    _libraryRow: {
      kind: 'preference', session_id: options.session || '',
      graph: { subject, predicate: 'prefers_tone', object: value, origin: options.origin || 'conversation', ...options.graph },
    },
  };
}

const rows = [
  record('user-new', 'User', '温暖专业'),
  record('user-copy', '用户', '温暖专业'),
  record('another-task', 'User', '温暖专业', { session: 'another-session' }),
  record('another-origin', 'User', '温暖专业', { origin: 'manual-source' }),
  record('client', '客户甲', '温暖专业'),
  record('named-client', 'AI Labs', '温暖专业'),
  record('assistant', 'AI', '中文'),
  record('named-agent', 'Researcher', '中文'),
  record('expired', 'User', '温暖专业', { graph: { valid_to: '2026-10-01' } }),
  record('future', 'User', '简短', { graph: { valid_from: '2026-11-01' } }),
];
const before = JSON.stringify(rows);
const archived = preferenceArchive(rows, ['Assistant', 'Researcher'], '2026-10-14');
assert.equal(archived.originalCount, 10);
assert.equal(archived.excluded, 2);
assert.equal(archived.current.length, 5, 'Display duplicates merge only within the same audience and source');
assert.equal(archived.current[0].members.length, 2);
assert.strictEqual(archived.current[0].members[1], rows[1], 'Original handles remain available');
assert.equal(archived.previous.length, 2, 'Recorded expiry and future dates stay separate from current claims');
assert.equal(JSON.stringify(rows), before, 'Presentation must not alter stored evidence or recall authority');
assert.ok(archived.current.some(item => item.members[0].id === 'named-client'), 'A client name containing AI is not an assistant');
assert.equal(preferenceStatement(rows[0]._libraryRow.graph, 'zh'), '用户希望表达语气为温暖专业');
assert.equal(preferenceStatement({ subject: '客户甲', predicate: 'prefers_report_format', object: 'Markdown' }, 'zh'), '客户甲希望报告使用Markdown');
assert.equal(preferenceStatement(rows[0]._libraryRow.graph, 'en'), 'The user prefers a tone of 温暖专业');
assert.equal(preferenceStatement({ subject: '客户乙', predicate: 'unmapped', object: '保留原表格' }, 'zh'), '客户乙有这样的要求：保留原表格');
const sessions = [{id: 'allowed-task'}];
const linked = record('linked', 'User', '中文', {session: 'allowed-task'})._libraryRow;
assert.deepEqual(Array.from(preferenceConversations(linked, sessions)), ['allowed-task'], 'A stored and currently authorized conversation is inspectable even without an exact quote');
assert.equal(preferenceConversations(linked, []).length, 0, 'No conversation link without current workspace and agent authorization');
assert.equal(preferenceConversations(record('foreign', 'User', '中文', {session: 'foreign-task'})._libraryRow, sessions).length, 0);
assert.equal(preferenceConversations(record('bare-origin', 'User', '中文', {origin: 'conversation:session:allowed-task'})._libraryRow, sessions).length, 0, 'Free-form source text must not create conversation authority');
const evidenceRow = {evidence: [{role: 'user', quote: '报告用中文', session_id: 'allowed-task'}, {role: 'tool', quote: 'untrusted', session_id: 'foreign-task'}], provenance: {origin: {session_id: 'allowed-task'}}};
assert.deepEqual(Array.from(preferenceConversations(evidenceRow, sessions)), ['allowed-task'], 'Distinct user evidence and provenance preserve a single source link');
const original = {session_id: 'allowed-task', messages: [
  {id: 1, role: 'user', content: '这次请说英文'},
  {id: 2, role: 'assistant', content: '用户偏好中文'},
  {id: 3, role: 'user', content: '先看原始对话'},
  {id: 4, role: 'tool', content: '用户偏好中文'},
]};
assert.deepEqual(Array.from(conversationUserMessages(original, 'allowed-task'), message => message.id), [1, 3], 'Original preview preserves user messages and their order, excluding assistant and tool claims');
assert.equal(conversationUserMessages(original, 'foreign-task'), null, 'A response for a different conversation must not be displayed');
assert.equal(conversationUserMessages({session_id: 'allowed-task'}, 'allowed-task'), null);
assert.deepEqual(Array.from(conversationMessages(original, 'allowed-task'), message => message.id), [1, 2, 3, 4], 'Complete conversation keeps user, AI and tool context');
assert.equal(conversationMessages(original, 'foreign-task'), null);
console.log('Preference presentation regression passed (grouping, evidence preservation, audience, dates, readable claims).');

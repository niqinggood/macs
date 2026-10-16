(function () {
  'use strict';
  function groups(source, sessionID) {
    if (source._libraryGroup) return [source._libraryGroup];
    if (['task_execution', 'task_insight'].includes(source.kind)) return ['tasks'];
    if (source.kind === 'tool_learning' || source.category === 'learning') return ['learning'];
    if (source.injection === 'system') return ['project'];
    if (source.category === 'preferences' || /:preference$/.test(source.kind || '')) return ['preferences'];
    if (['project', 'global'].includes(source.category)) return ['project'];
    if (source.kind === 'summary' && source.mode === 'relevant' && (!sessionID || source.source_session_id && source.source_session_id !== sessionID)) return ['learning'];
    if (source.injection === 'history_messages' || ['short', 'session'].includes(source.category) || source.kind === 'session_context' || source.kind === 'summary') return ['short'];
    if (source.category === 'knowledge' || ['learning_knowledge', 'knowledge_fact'].includes(source.kind)) return ['knowledge'];
    return ['learning'];
  }
  function content(source, current) {
    if (source._libraryContent !== undefined) return source._libraryContent;
    const item = current && Object.hasOwn(current, source.id) ? current[source.id] : null;
    return typeof item?.content === 'string' ? item.content : source.excerpt || '';
  }
  function body(source, current) {
    let value = content(source, current).replace(/\r\n/g, '\n').trim();
    if (/\.insights$/.test(source.source_label || '')) {
      try { const items = JSON.parse(value); if (Array.isArray(items) && items.every(item => typeof item === 'string')) value = items.join('\n').trim(); } catch (_) {}
    }
    return value;
  }
  function assemble(sources, current, group, sessionID) {
    const records = [], indexed = new Map();
    for (const source of sources) {
      if (group === 'all' ? groups(source, sessionID).includes('short') : !groups(source, sessionID).includes(group)) continue;
      const event = /^memory_events#([^\s.]+)\.(?:final_answer|learn_note|insights)$/.exec(source.source_label || '');
      // Combine equal representations of the same original task for display.
      // The real handles and individual fields remain available for editing.
      const key = event ? 'event:' + event[1] + ':' + body(source, current) : source._libraryKey || source.source_label || source.id;
      const record = indexed.get(key);
      if (record) { record.members.push(source); continue; }
      const created = {key, members:[source]}; indexed.set(key, created); records.push(created);
    }
    return records;
  }
  function brief(value, length) {
    return String(value || '').replace(/^#{1,6}\s+/gm, '').replace(/\*\*/g, '').replace(/\s+/g, ' ').trim().slice(0, length || 100);
  }
  function factStatus(graph, asOf) {
    if (!graph) return null;
    const today = asOf || new Date().toISOString().slice(0, 10);
    const date = value => /^\d{4}-\d{2}-\d{2}$/.test(value || '') ? value : '';
    const end = date(graph.valid_to), start = date(graph.valid_from);
    return {expired: !!end && end <= today, future: !!start && start > today, evidence: graph.evidence_availability || 'missing'};
  }
  function preferenceArchive(sources, agentNames, asOf) {
    const normalize = value => String(value || '').normalize('NFKC').trim().toLowerCase();
    const assistants = new Set(['ai', 'assistant', 'agent', 'bot', '助手', 'ai 助手', 'ai助手', '人工智能', '智能助手', ...(agentNames || []).map(normalize)]);
    const current = new Map(), previous = new Map();
    let excluded = 0, originalCount = 0;
    for (const source of sources) {
      const graph = source._libraryRow?.graph;
      if (source._libraryRow?.kind !== 'preference' || !graph) continue;
      originalCount++;
      if (assistants.has(normalize(graph.subject))) { excluded++; continue; }
      const subject = /^(user|用户|human)$/.test(normalize(graph.subject)) ? 'user' : normalize(graph.subject);
      // Only group display-equivalent claims within the same original audience.
      // Every original row remains selectable; grouping never changes recall.
      const key = JSON.stringify([subject, normalize(graph.predicate), normalize(graph.object), graph.origin || '', source._libraryRow.session_id || '']);
      const status = factStatus(graph, asOf), target = status.expired || status.future ? previous : current;
      if (target.has(key)) target.get(key).members.push(source);
      else target.set(key, {key, members: [source]});
    }
    return {current: [...current.values()], previous: [...previous.values()], excluded, originalCount};
  }
  function preferenceStatement(graph, language) {
    const subject = String(graph?.subject || '').trim(), object = String(graph?.object || '').trim();
    const generic = /^(user|用户|human)$/i.test(subject);
    const who = language === 'en' ? generic ? 'The user' : subject : generic ? '用户' : subject;
    const formats = language === 'en' ? {
      prefers_report_format: ' prefers reports in ', prefers_tone: ' prefers a tone of ', prefers_style: ' prefers the style ',
      language_preference: ' prefers the language ', prefers_language: ' prefers the language ', prefers_tool: ' prefers using ',
      testing_framework_preference: ' prefers these testing tools: ', transportation_preference: ' prefers travelling by ',
      dislikes: ' dislikes ', prefers: ' prefers '
    } : {
      prefers_report_format: '希望报告使用', prefers_tone: '希望表达语气为', prefers_style: '希望表达风格为',
      language_preference: '希望使用的语言是', prefers_language: '希望使用的语言是', prefers_tool: '希望使用的工具是',
      testing_framework_preference: '希望使用的测试工具是', transportation_preference: '希望使用的出行方式是',
      dislikes: '不喜欢', prefers: '偏好'
    };
    return who + (formats[graph?.predicate] || (language === 'en' ? ' has this recorded requirement: ' : '有这样的要求：')) + object;
  }
  function preferenceConversations(row, sessions) {
    const allowed = new Set((sessions || []).map(session => session.id)), found = new Set();
    const add = id => { if (typeof id === 'string' && allowed.has(id)) found.add(id); };
    for (const proof of row?.evidence || []) if (proof.role === 'user' && proof.quote) add(proof.session_id);
    add(row?.provenance?.origin?.session_id);
    if (row?.graph) add(row.session_id);
    if (row?.source_quote) add(row.source_session_id);
    // A free-form graph origin or matching timestamp cannot establish a source.
    return [...found];
  }
  function conversationMessages(data, sessionID) {
    if (!data || data.session_id !== sessionID || !Array.isArray(data.messages)) return null;
    return data.messages.filter(message => message && ['user','assistant','tool','system'].includes(message.role) && typeof message.content === 'string');
  }
  function conversationUserMessages(data, sessionID) { const messages = conversationMessages(data, sessionID); return messages && messages.filter(message => message.role === 'user'); }
  window.MacsMemoryGroups = Object.freeze({groups, content, assemble, brief, factStatus, preferenceArchive, preferenceStatement, preferenceConversations, conversationMessages, conversationUserMessages});
})();

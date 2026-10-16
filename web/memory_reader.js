(function () {
  'use strict';
  const node = (tag, value) => { const item = document.createElement(tag); if (value !== undefined) item.textContent = value; return item; };
  // Read existing Markdown tokens through DOM text nodes. Stored HTML, images
  // and links never create active content or automatic network requests.
  function render(value) {
    const article = node('article'); article.className = 'knowledge-document';
    const text = String(value || '');
    if (!window.marked?.lexer) { article.append(node('p', text)); return article; }
    let count = 0;
    function append(parent, tokens, depth) {
      for (const token of tokens || []) {
        if (++count > 3000 || depth > 12) { parent.append(document.createTextNode(token.raw || token.text || '')); continue; }
        let item;
        switch (token.type) {
          case 'space': continue;
          case 'heading': item = node('h' + Math.min(6, Math.max(4, token.depth + 3))); break;
          case 'paragraph': item = node('p'); break;
          case 'strong': item = node('strong'); break;
          case 'em': item = node('em'); break;
          case 'del': item = node('s'); break;
          case 'codespan': item = node('code', token.text); break;
          case 'code': item = node('pre'); item.append(node('code', token.text)); break;
          case 'br': item = node('br'); break;
          case 'hr': item = node('hr'); break;
          case 'blockquote': item = node('blockquote'); break;
          case 'list':
            item = node(token.ordered ? 'ol' : 'ul');
            if (token.ordered && Number.isSafeInteger(token.start)) item.start = token.start;
            for (const entry of token.items || []) { const row = node('li'); append(row, entry.tokens || [{type:'text',text:entry.text}], depth + 1); item.append(row); }
            break;
          case 'table': {
            item = node('div'); item.className = 'knowledge-table'; const table = node('table'), head = node('tr'), body = node('tbody');
            for (const cell of token.header || []) { const col = node('th'); append(col, cell.tokens || [{type:'text',text:cell.text}], depth + 1); head.append(col); }
            const header = node('thead'); header.append(head); table.append(header);
            for (const cells of token.rows || []) { const row = node('tr'); for (const cell of cells) { const col = node('td'); append(col, cell.tokens || [{type:'text',text:cell.text}], depth + 1); row.append(col); } body.append(row); }
            table.append(body); item.append(table); break;
          }
          case 'link': item = node('span'); append(item, token.tokens || [{type:'text',text:token.text}], depth + 1); if (token.href) item.append(document.createTextNode(' (' + token.href + ')')); break;
          case 'image': item = node('span', token.text || token.href || ''); break;
          case 'html': item = node('span', token.raw || token.text || ''); break;
          case 'text':
            if (token.tokens) { append(parent, token.tokens, depth + 1); continue; }
            parent.append(document.createTextNode(token.text || '')); continue;
          default: parent.append(document.createTextNode(token.raw || token.text || '')); continue;
        }
        if (token.tokens && !['list','table','link','image','html','code','codespan'].includes(token.type)) append(item, token.tokens, depth + 1);
        else if (['heading','paragraph','blockquote'].includes(token.type) && !token.tokens) item.textContent = token.text || '';
        parent.append(item);
      }
    }
    try { append(article, window.marked.lexer(text), 0); }
    catch (_) { article.replaceChildren(node('p', text)); }
    return article;
  }
  window.MacsMemoryReader = Object.freeze({render});
})();

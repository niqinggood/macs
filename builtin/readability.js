#!/usr/bin/env node
/**
 * Mozilla Readability extractor — called by Go web_fetch for clean content extraction.
 * Usage: node readability.js < input.html > output.md
 * Or: node readability.js "https://example.com"
 */
const { JSDOM } = require("jsdom");
const { Readability } = require("@mozilla/readability");
const fs = require("fs");

async function getHTML(input) {
    if (input && /^https?:\/\//.test(input)) {
        // Fetch from URL
        const resp = await fetch(input, {
            headers: { "User-Agent": "Mozilla/5.0 (compatible; openkitty/1.0)" },
            redirect: "follow"
        });
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        return await resp.text();
    }
    // Read from stdin
    return fs.readFileSync(0, "utf-8");
}

async function main() {
    const input = process.argv[2] || "";
    let html;

    try {
        html = await getHTML(input);
    } catch (e) {
        process.stderr.write(`Readability error: ${e.message}\n`);
        process.exit(1);
    }

    const doc = new JSDOM(html, { url: input || "about:blank" });
    const reader = new Readability(doc.window.document);
    const article = reader.parse();

    if (!article || !article.content) {
        // Fallback: extract body text
        const body = doc.window.document.body;
        if (body) {
            console.log(`# ${doc.window.document.title || "Untitled"}\n\n${body.textContent.trim()}`);
        }
        process.exit(0);
    }

    // Output clean markdown: title + extracted content
    const title = article.title || doc.window.document.title || "Untitled";
    // Strip HTML tags from content, keep basic formatting
    const content = article.content
        // 保留图片: <img src="..."> → ![image](src) (修复照片丢失问题)
        .replace(/<img[^>]+src="([^"]+)"[^>]*>/gi, "![image]($1)")
        .replace(/<img[^>]+src='([^']+)'[^>]*>/gi, "![image]($1)")
        .replace(/<h[1-6][^>]*>/g, "\n\n## ")
        .replace(/<\/h[1-6]>/g, "\n\n")
        .replace(/<p[^>]*>/g, "\n")
        .replace(/<\/p>/g, "\n")
        .replace(/<br\s*\/?>/g, "\n")
        .replace(/<li[^>]*>/g, "\n- ")
        .replace(/<\/li>/g, "")
        .replace(/<[^>]+>/g, "")
        .replace(/\n{3,}/g, "\n\n")
        .trim();

    console.log(`# ${title}\n\n${content}`);
}

main().catch(e => { process.stderr.write(`${e.message}\n`); process.exit(1); });

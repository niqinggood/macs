#!/usr/bin/env node
/**
 * Puppeteer screenshot tool — captures webpage screenshots.
 * Usage: node screenshot.js <url> <output.png> [width] [fullPage:true|false]
 */
const puppeteer = require("puppeteer");

async function main() {
    const [url, output, width, fullPage] = process.argv.slice(2);
    if (!url || !output) {
        console.error("Usage: node screenshot.js <url> <output.png> [width] [fullPage]");
        process.exit(1);
    }

    const browser = await puppeteer.launch({
        headless: true,
        args: ["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"]
    });

    try {
        const page = await browser.newPage();
        await page.setViewport({
            width: parseInt(width) || 1440,
            height: 900
        });
        await page.goto(url, { waitUntil: "networkidle2", timeout: 25000 });
        await page.screenshot({
            path: output,
            fullPage: fullPage !== "false"
        });
        console.log(`Screenshot saved: ${output}`);
    } finally {
        await browser.close();
    }
}
main().catch(e => { console.error(e.message); process.exit(1); });

#!/usr/bin/env node
/**
 * Sharp image processor — resize, convert, compress.
 * Usage: node image_process.js <input> <output> [width] [height] [quality:1-100]
 */
const sharp = require("sharp");
const fs = require("fs");

async function main() {
    const [input, output, width, height, quality] = process.argv.slice(2);
    if (!input || !output) {
        console.error("Usage: node image_process.js <input> <output> [width] [height] [quality]");
        process.exit(1);
    }

    if (!fs.existsSync(input)) {
        console.error(`File not found: ${input}`);
        process.exit(1);
    }

    let pipeline = sharp(input);
    const metadata = await pipeline.metadata();

    if (width || height) {
        pipeline = pipeline.resize(
            width ? parseInt(width) : null,
            height ? parseInt(height) : null,
            { fit: "inside", withoutEnlargement: true }
        );
    }

    const ext = output.split(".").pop().toLowerCase();
    switch (ext) {
        case "jpg": case "jpeg":
            pipeline = pipeline.jpeg({ quality: parseInt(quality) || 85 }); break;
        case "png":
            pipeline = pipeline.png({ quality: parseInt(quality) || 90 }); break;
        case "webp":
            pipeline = pipeline.webp({ quality: parseInt(quality) || 80 }); break;
        case "avif":
            pipeline = pipeline.avif({ quality: parseInt(quality) || 70 }); break;
    }

    await pipeline.toFile(output);
    const stat = fs.statSync(output);
    console.log(`Image processed: ${output} (${(stat.size/1024).toFixed(1)}KB, ${metadata.format}→${ext})`);
}
main().catch(e => { console.error(e.message); process.exit(1); });

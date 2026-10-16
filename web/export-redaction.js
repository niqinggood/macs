(function (global) {
    'use strict';

    var exportAccountPatterns = [
        [/(["']?(?:api[_-]?key|apikey|api[_-]?secret|secret[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|authorization|cookie|x[_-]?api[_-]?key|token)["']?\s*[:=]\s*)(["'])[^"'\r\n]{8,}(["'])/gi, '$1$2[REDACTED]$3'],
        [/(\b(?:api[_-]?key|apikey|api[_-]?secret|secret[_-]?key|access[_-]?token|refresh[_-]?token|client[_-]?secret|password|authorization|cookie|x[_-]?api[_-]?key|token)\b\s*[:=]\s*)[^\s,"';}\]\[][^\s,"';}\]]{7,}/gi, '$1[REDACTED]'],
        [/sk-(?:[a-z0-9]{1,15}[._-]){0,8}[a-z0-9_]{16,}[a-z0-9._-]*/gi, 'sk-[REDACTED]'],
        [/\bbearer\s+[a-z0-9._~+/=-]{12,}/gi, 'Bearer [REDACTED]'],
        [/-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/gi, '[REDACTED PRIVATE KEY]'],
        [/\beyJ[a-zA-Z0-9_-]{5,}\.[a-zA-Z0-9_-]{5,}\.[a-zA-Z0-9_-]{5,}\b/g, '[REDACTED JWT]'],
        [/\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b/gi, '[REDACTED EMAIL]'],
        [/\b1[3-9][0-9]{9}\b/g, '[REDACTED PHONE]'],
        [/(["']?(?:(?:\b(?:account(?:[_-]?id)?|user[_-]?(?:name|id)|login(?:[_-]?(?:name|id))?|uid|tenant[_-]?id|subject[_-]?id|e-?mail|phone|mobile|tel)\b)|(?:账号|用户名|用户id|登录名|手机号|邮箱|电话|手机))["']?\s*[:=]\s*)(["'])([^"'\r\n\[])[^"'\r\n]{2,}(["'])/gi, '$1$2[REDACTED]$4'],
        [/((?:(?:\b(?:account(?:[_-]?id)?|user[_-]?(?:name|id)|login(?:[_-]?(?:name|id))?|uid|tenant[_-]?id|subject[_-]?id|e-?mail|phone|mobile|tel)\b)|(?:账号|用户名|用户id|登录名|手机号|邮箱|电话|手机))\s*[:=]\s*)[^\s,"';}\]\[][^\s,"';}\]]{2,}/gi, '$1[REDACTED]']
    ];

    global.redactAccountInfoForExport = function (value) {
        var output = String(value == null ? '' : value);
        exportAccountPatterns.forEach(function (entry) {
            output = output.replace(entry[0], entry[1]);
        });
        return output;
    };

    global.redactAccountInfoInExportDOM = function (root) {
        if (!root) return function () {};
        var changes = [];
        var owner = root.ownerDocument || document;
        var walker = owner.createTreeWalker(root, NodeFilter.SHOW_TEXT);
        var node;
        while ((node = walker.nextNode())) {
            var redacted = global.redactAccountInfoForExport(node.nodeValue);
            if (redacted !== node.nodeValue) {
                changes.push([node, node.nodeValue]);
                node.nodeValue = redacted;
            }
        }
        return function () {
            changes.forEach(function (entry) { entry[0].nodeValue = entry[1]; });
        };
    };
})(window);

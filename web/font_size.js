(function () {
    'use strict';
    var key = 'macs.ui.fontSize';
    var options = [100, 110, 120, 130];
    var names = ['fontSizeStandard', 'fontSizeComfort', 'fontSizeLarge', 'fontSizeExtraLarge'];
    var fallback = {zh: ['标准', '舒适', '大字', '更大'], en: ['Standard', 'Comfort', 'Large', 'Extra large']};
    var size = 110, trigger, menu;

    function t(name, text) { return typeof window.openKittyT === 'function' ? window.openKittyT(name, text) : text; }
    function valid(value) { return options.indexOf(Number(value)) >= 0; }
    function apply(value) {
        size = valid(value) ? Number(value) : 110;
        // Source sizes use the comfortable 110% baseline; scale only fonts.
        document.documentElement.style.setProperty('--macs-font-scale', String(size / 110));
        document.documentElement.dataset.fontSize = String(size);
        if (trigger) render();
        document.dispatchEvent(new CustomEvent('openkitty:fontsizechange', {detail: {percent: size}}));
    }
    try { apply(localStorage.getItem(key)); } catch (error) { apply(110); }

    function close(returnFocus) {
        if (!menu) return;
        menu.hidden = true;
        trigger.setAttribute('aria-expanded', 'false');
        if (returnFocus) trigger.focus({preventScroll: true});
    }
    function position() {
        if (!menu || menu.hidden) return;
        var box = trigger.getBoundingClientRect();
        menu.style.left = Math.max(8, Math.min(box.right - menu.offsetWidth, innerWidth - menu.offsetWidth - 8)) + 'px';
        menu.style.top = Math.max(8, Math.min(box.bottom + 8, innerHeight - menu.offsetHeight - 8)) + 'px';
    }
    function render() {
        var english = String(window.openKittyLanguage || document.documentElement.lang).indexOf('en') === 0;
        var language = english ? 'en' : 'zh';
        var label = t('fontSizeLabel', english ? 'Font size' : '字体大小');
        trigger.title = label + ' · ' + size + '%';
        trigger.setAttribute('aria-label', trigger.title);
        menu.setAttribute('aria-label', label);
        menu.replaceChildren();
        options.forEach(function (value, index) {
            var button = document.createElement('button');
            button.type = 'button';
            button.setAttribute('role', 'menuitemradio');
            button.setAttribute('aria-checked', String(value === size));
            button.dataset.fontPercent = String(value);
            var name = document.createElement('span');
            name.textContent = t(names[index], fallback[language][index]);
            var amount = document.createElement('span');
            amount.className = 'font-size-percent'; amount.textContent = value + '%';
            button.append(name, amount);
            button.addEventListener('click', function () {
                apply(value);
                try { localStorage.setItem(key, String(size)); }
                catch (error) {
                    if (typeof window.showToast === 'function') window.showToast(t('fontSizeSaveFailed', english ? 'Font size changed for this visit; this browser could not save it.' : '本次字号已调整，但浏览器未能保存。'), 'info');
                }
                close(true);
            });
            menu.appendChild(button);
        });
        position();
    }
    function open() {
        render(); menu.hidden = false;
        trigger.setAttribute('aria-expanded', 'true'); position();
        menu.querySelector('[aria-checked="true"]').focus({preventScroll: true});
    }
    function init() {
        trigger = document.getElementById('fontSizeToggle');
        if (!trigger) return;
        menu = document.createElement('div'); menu.id = 'fontSizeMenu';
        menu.className = 'font-size-menu'; menu.hidden = true; menu.setAttribute('role', 'menu');
        document.body.appendChild(menu); render();
        trigger.addEventListener('click', function () { if (menu.hidden) open(); else close(true); });
        trigger.addEventListener('keydown', function (event) { if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); open(); } });
        document.addEventListener('click', function (event) { if (!menu.hidden && !menu.contains(event.target) && !trigger.contains(event.target)) close(false); });
        document.addEventListener('keydown', function (event) {
            if (menu.hidden) return;
            if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); close(true); return; }
            if (event.key === 'Tab') { close(false); return; }
            if (['ArrowDown', 'ArrowUp', 'Home', 'End'].indexOf(event.key) < 0) return;
            event.preventDefault();
            var buttons = Array.from(menu.querySelectorAll('button'));
            var index = buttons.indexOf(document.activeElement);
            var next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : buttons.length - 1)) % buttons.length;
            buttons[next].focus();
        }, true);
        window.addEventListener('resize', position);
        window.addEventListener('scroll', position, true);
        window.addEventListener('storage', function (event) { if (event.key === key || event.key === null) apply(event.newValue); });
        document.addEventListener('openkitty:languagechange', function () {
            var selected = !menu.hidden && document.activeElement && document.activeElement.dataset.fontPercent;
            render(); if (selected) menu.querySelector('[data-font-percent="' + selected + '"]').focus({preventScroll: true});
        });
    }
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init, {once: true}); else init();
})();

import { JoinApp } from './components/JoinApp.js';
import { registerFocusTrap } from '@shared/focusTrap.js';
import { registerI18n } from './util/i18n.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('joinApp', () => new JoinApp());
    registerFocusTrap(Alpine);
    registerI18n(Alpine);
});

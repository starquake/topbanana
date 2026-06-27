import { JoinApp } from './components/JoinApp.js';
import { registerFocusTrap } from '@shared/focusTrap.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('joinApp', () => new JoinApp());
    registerFocusTrap(Alpine);
});

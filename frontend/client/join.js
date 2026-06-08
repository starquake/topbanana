import { JoinApp } from './components/JoinApp.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('joinApp', () => new JoinApp());
});

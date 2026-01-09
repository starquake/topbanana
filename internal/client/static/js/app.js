import { GameApp } from './components/GameApp.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('gameApp', () => new GameApp());
});

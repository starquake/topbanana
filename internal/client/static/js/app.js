import { GameApp } from './components/GameApp.js';
import { claimNameForm } from './components/ClaimNameForm.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('gameApp', () => new GameApp());
    // claimNameForm is a per-instance Alpine subcomponent. Each x-data
    // call gets its own input value / submitting / error state, which
    // is why we register it as a factory rather than constructing it
    // alongside gameApp.
    Alpine.data('claimNameForm', claimNameForm);
});

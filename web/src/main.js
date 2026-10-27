import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';

mount(App, { target: document.getElementById('app') });

// The static demo (GitHub Pages) says so on every page.
if (import.meta.env.VITE_DEMO === '1') import('./demo/banner.js');

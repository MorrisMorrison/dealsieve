// @ts-check
import { defineConfig, passthroughImageService } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';

// GitHub Pages project site, served at https://<user>.github.io/dealsieve/
const site = process.env.DOCS_SITE ?? 'https://morrismorrison.github.io';
const base = process.env.DOCS_BASE ?? '/dealsieve';
const repo = 'https://github.com/MorrisMorrison/dealsieve';

export default defineConfig({
	site,
	base,
	image: { service: passthroughImageService() },
	integrations: [
		starlight({
			title: 'dealsieve',
			logo: { src: './src/assets/logo.svg', alt: '' },
			description:
				'A small, self-hosted filter for deal feeds. It drops recurring pseudo-deals and noise and shows what is left, with reasons.',
			favicon: '/favicon.svg',
			customCss: ['./src/styles/custom.css'],
			social: [{ icon: 'github', label: 'GitHub', href: repo }],
			editLink: { baseUrl: `${repo}/edit/main/docs-site/` },
			// Fails the build on a broken internal link or anchor. The docs mention
			// http://localhost:8080, so local links are allowed.
			plugins: [starlightLinksValidator({ errorOnLocalLinks: false })],
			sidebar: [
				{ label: 'Guide', items: ['guide/how-it-works', 'guide/install', 'guide/notifications'] },
				{ label: 'Reference', items: ['reference/configuration', 'reference/api'] },
				{ label: 'Project', items: ['project/development'] }
			]
		})
	]
});

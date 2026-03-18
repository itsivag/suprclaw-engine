// @ts-check
// `@type` JSDoc annotations allow editor autocompletion and type checking
// (when paired with `@ts-check`).
// There are various equivalent ways to declare your Docusaurus config.
// See: https://docusaurus.io/docs/api/docusaurus-config

import { themes as prismThemes } from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'SuprClaw',
  tagline: 'Ultra-lightweight personal AI assistant. <10MB RAM, 1-second boot, single binary.',
  favicon: 'img/favicon.ico',

  // Set the production url of your site here
  url: 'https://itsivag.github.io',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/suprclaw-engine/',

  // GitHub pages deployment config.
  // If you aren't using GitHub pages, you don't need these.
  organizationName: 'itsivag', // Usually your GitHub org/user name.
  projectName: 'suprclaw-engine', // Usually your repo name.
  trailingSlash: false,

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: './sidebars.js',
          // Please change this to your repo.
          // Remove this to remove the "edit this page" links.
          editUrl: 'https://github.com/itsivag/suprclaw-engine/tree/main/website/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      // Replace with your project's social card
      image: 'img/suprclaw-social-card.png',
      colorMode: {
        defaultMode: 'dark',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
      navbar: {
        title: 'SuprClaw',
        logo: {
          alt: 'SuprClaw Logo',
          src: 'img/logo.svg',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'tutorialSidebar',
            position: 'left',
            label: 'Docs',
          },
          {
            href: 'https://github.com/itsivag/suprclaw-engine',
            label: 'GitHub',
            position: 'right',
          },
          {
            href: 'https://github.com/itsivag/suprclaw-engine/releases',
            label: 'Releases',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              { label: 'Getting Started', to: '/docs/intro' },
              { label: 'Installation', to: '/docs/installation' },
              { label: 'Configuration', to: '/docs/configuration/overview' },
            ],
          },
          {
            title: 'Channels',
            items: [
              { label: 'Telegram', to: '/docs/channels/telegram' },
              { label: 'Discord', to: '/docs/channels/discord' },
              { label: 'WhatsApp', to: '/docs/channels/whatsapp' },
              { label: 'Matrix', to: '/docs/channels/matrix' },
            ],
          },
          {
            title: 'More',
            items: [
              {
                label: 'GitHub',
                href: 'https://github.com/itsivag/suprclaw-engine',
              },
              {
                label: 'Releases',
                href: 'https://github.com/itsivag/suprclaw-engine/releases',
              },
              {
                label: 'Issues',
                href: 'https://github.com/itsivag/suprclaw-engine/issues',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} itsivag. Built with Docusaurus.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['bash', 'json', 'go'],
      },
    }),
};

export default config;

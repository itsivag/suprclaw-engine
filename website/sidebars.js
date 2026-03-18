// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  tutorialSidebar: [
    {
      type: 'doc',
      id: 'intro',
      label: 'Introduction',
    },
    {
      type: 'doc',
      id: 'installation',
      label: 'Installation',
    },
    {
      type: 'doc',
      id: 'quick-start',
      label: 'Quick Start',
    },
    {
      type: 'category',
      label: 'Configuration',
      items: [
        'configuration/overview',
        'configuration/providers',
        'configuration/tools',
        'configuration/credential-encryption',
      ],
    },
    {
      type: 'category',
      label: 'Chat Channels',
      items: [
        'channels/overview',
        'channels/telegram',
        'channels/discord',
        'channels/whatsapp',
        'channels/matrix',
        'channels/line',
      ],
    },
    {
      type: 'category',
      label: 'Advanced',
      items: [
        'advanced/mcp',
        'advanced/scheduled-tasks',
        'advanced/security-sandbox',
        'advanced/antigravity',
        'advanced/creating-providers',
      ],
    },
    {
      type: 'category',
      label: 'Reference',
      items: [
        'reference/cli',
        'reference/env-vars',
        'reference/troubleshooting',
      ],
    },
  ],
};

export default sidebars;

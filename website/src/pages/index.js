import clsx from 'clsx';
import { useState } from 'react';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

const INSTALL_CMD = 'curl -fsSL https://raw.githubusercontent.com/itsivag/suprclaw-engine/main/install.sh | sh';

function InstallBlock() {
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    navigator.clipboard.writeText(INSTALL_CMD).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };
  return (
    <div className={styles.installBlock}>
      <code>{INSTALL_CMD}</code>
      <button className={styles.copyBtn} onClick={handleCopy} title="Copy to clipboard">
        {copied ? '✓' : '⎘'}
      </button>
    </div>
  );
}

const features = [
  {
    title: '<10MB RAM',
    description: '99% lighter than Electron-based alternatives. Runs on $10 hardware with a 0.6GHz single-core CPU.',
  },
  {
    title: '1-Second Boot',
    description: 'Single binary, no runtime dependencies. Drop it anywhere and it just works.',
  },
  {
    title: 'Multi-Architecture',
    description: 'Native builds for x86_64, ARM64, ARMv7, MIPS, RISC-V, and LoongArch.',
  },
  {
    title: 'Multi-Channel',
    description: 'Connect to Telegram, Discord, WhatsApp, Matrix, LINE, and more via the built-in gateway.',
  },
  {
    title: 'Multi-Provider',
    description: 'Works with Anthropic, OpenAI, Gemini, Groq, Ollama, OpenRouter, Azure, and many more.',
  },
  {
    title: 'Self-Hosted',
    description: 'No telemetry, no tracking. All data stays local. You own your data.',
  },
];

function Feature({ title, description }) {
  return (
    <div className={clsx('col col--4', styles.feature)}>
      <div className="padding-horiz--md padding-vert--sm">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

function HomepageHeader() {
  const { siteConfig } = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        <div className={styles.buttons}>
          <Link
            className="button button--secondary button--lg"
            to="/docs/intro">
            Get Started
          </Link>
          <Link
            className="button button--outline button--secondary button--lg"
            href="https://github.com/itsivag/suprclaw-engine/releases">
            Download
          </Link>
        </div>
        <InstallBlock />
      </div>
    </header>
  );
}

export default function Home() {
  return (
    <Layout
      title="Ultra-lightweight AI assistant"
      description="SuprClaw is an ultra-lightweight personal AI assistant written in Go. Runs on $10 hardware with <10MB RAM, 1-second boot, single binary.">
      <HomepageHeader />
      <main>
        <section className={styles.features}>
          <div className="container">
            <div className="row">
              {features.map((props, idx) => (
                <Feature key={idx} {...props} />
              ))}
            </div>
          </div>
        </section>
      </main>
    </Layout>
  );
}

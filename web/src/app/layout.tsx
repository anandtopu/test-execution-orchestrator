import type { Metadata } from 'next';
import './globals.css';
import '../styles/teo-tokens.css';
import '../styles/teo.css';
import { Shell } from '@/components/teo/Shell';

export const metadata: Metadata = {
  title: 'TEO · Test Execution Orchestrator',
  description: 'Test Execution Orchestrator',
};

// Apply the persisted theme before first paint so dark/contrast users don't
// flash light. Mirrors the data-theme contract the Shell's switcher writes.
const themeInit = `(function(){try{var t=localStorage.getItem('teo-theme');if(t==='dark'||t==='contrast'||t==='light'){document.documentElement.setAttribute('data-theme',t);}}catch(e){}})();`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" data-theme="light" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeInit }} />
      </head>
      <body>
        <Shell>{children}</Shell>
      </body>
    </html>
  );
}

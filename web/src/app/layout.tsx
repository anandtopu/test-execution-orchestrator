import type { Metadata } from 'next';
import './globals.css';
import { SessionNav } from '@/components/SessionNav';

export const metadata: Metadata = {
  title: 'TEO',
  description: 'Test Execution Orchestrator',
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header className="border-b">
          <nav className="mx-auto flex max-w-6xl items-center justify-between px-6 py-3">
            <a href="/" className="text-lg font-semibold">TEO</a>
            <ul className="flex items-center gap-6 text-sm">
              <li><a href="/runs">Runs</a></li>
              <li><a href="/tests">Tests</a></li>
              <li><a href="/clusters">Failures</a></li>
              <li><a href="/flakes">Flakes</a></li>
              <li><a href="/cost">Cost</a></li>
              <li><SessionNav /></li>
            </ul>
          </nav>
        </header>
        <main className="mx-auto max-w-6xl px-6 py-6">{children}</main>
      </body>
    </html>
  );
}

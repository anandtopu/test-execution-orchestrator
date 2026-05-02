export default function HomePage() {
  return (
    <div>
      <h1 className="text-2xl font-semibold">TEO</h1>
      <p className="mt-2 text-sm text-gray-600">
        Test Execution Orchestrator. Pick a section above.
      </p>
      <ul className="mt-6 space-y-2 text-sm">
        <li>
          <a className="text-blue-600 underline" href="/runs">Recent runs</a>
          {" — see what's running and why builds are red."}
        </li>
        <li>
          <a className="text-blue-600 underline" href="/clusters">Failure clusters</a>
          {' — every distinct error grouped by stack fingerprint.'}
        </li>
        <li>
          <a className="text-blue-600 underline" href="/flakes">Flakes</a>
          {' — Wilson-confirmed flaky tests, with quarantine status.'}
        </li>
      </ul>
    </div>
  );
}

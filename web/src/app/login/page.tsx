// Sign-in landing page (S-03-02 / FR-801). The button hands off to the API's
// OIDC login route, which redirects to the configured IdP (Dex). In the
// standard same-origin deployment that's the relative /auth/login; set
// NEXT_PUBLIC_API_BASE for a split-origin dev setup.
const apiBase = process.env.NEXT_PUBLIC_API_BASE ?? '';

export default function LoginPage() {
  return (
    <div className="mx-auto max-w-sm py-20 text-center">
      <h1 className="text-2xl font-semibold">Sign in to TEO</h1>
      <p className="mt-2 text-sm text-gray-500">
        Authenticate with your organization&apos;s single sign-on.
      </p>
      <a
        href={`${apiBase}/auth/login`}
        className="mt-6 inline-block rounded bg-blue-600 px-5 py-2 text-sm font-medium text-white hover:bg-blue-700"
      >
        Sign in with SSO
      </a>
    </div>
  );
}

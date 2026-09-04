import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import reactHooks from 'eslint-plugin-react-hooks'
import globals from 'globals'

export default tseslint.config(
  {
    ignores: ['dist', 'src/routeTree.gen.ts', 'src/paraglide', 'test-results'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks },
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    rules: {
      ...reactHooks.configs.recommended.rules,
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      // Defense-in-depth for the go-rewrite-08 split: `web/tsconfig.json`'s `#/*` path already
      // only resolves inside `web/src`, so a `#/server/...` import fails to resolve rather than
      // reaching the backend-reference code left behind at the repo root — this rule just makes
      // the intent explicit and gives a clearer message than a bare "cannot find module".
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            { group: ['#/server/*', '#/server'], message: 'web/ may not import root src/server — that is backend-reference code staying behind for the Go rewrite (see go-rewrite-08 task 1).' },
            { group: ['#/do/*', '#/do'], message: 'web/ may not import root src/do — that is backend-reference code staying behind for the Go rewrite (see go-rewrite-08 task 1).' },
            { group: ['#/rooms/*', '#/rooms'], message: 'web/ may not import root src/rooms — that is backend-reference code staying behind for the Go rewrite (see go-rewrite-08 task 1).' },
            { group: ['#/routes/api/*', '#/routes/api'], message: 'web/ may not import root src/routes/api — that is backend-reference code staying behind for the Go rewrite (see go-rewrite-08 task 1).' },
          ],
        },
      ],
    },
  },
)

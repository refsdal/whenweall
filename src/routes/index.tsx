import { createFileRoute } from '@tanstack/react-router'
import { LocaleSwitcher } from '#/components/layout/LocaleSwitcher'

export const Route = createFileRoute('/')({ component: App })

function App() {
  return (
    <main className="p-8">
      <h1 className="text-3xl font-bold">samla</h1>
      {/* Temporary placement for dev exercising; Task 15 moves this into the Header/Footer. */}
      <LocaleSwitcher />
    </main>
  )
}

import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/')({ component: App })

function App() {
  return (
    <main className="p-8">
      <h1 className="text-3xl font-bold">samla</h1>
    </main>
  )
}

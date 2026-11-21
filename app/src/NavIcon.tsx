// Line icons for the side nav, drawn on a 24px grid with the text colour.
const paths: Record<string, string> = {
  nova: 'M12 3c.6 4.2 1.8 5.4 6 6-4.2.6-5.4 1.8-6 6-.6-4.2-1.8-5.4-6-6 4.2-.6 5.4-1.8 6-6zM18.5 15.5c.3 1.6.9 2.2 2.5 2.5-1.6.3-2.2.9-2.5 2.5-.3-1.6-.9-2.2-2.5-2.5 1.6-.3 2.2-.9 2.5-2.5z',
  mind: 'M9 4.5a3 3 0 0 0-3 3 3 3 0 0 0-2 5.2A3 3 0 0 0 6.5 18 2.5 2.5 0 0 0 12 19V5.5A2.5 2.5 0 0 0 9 4.5zM15 4.5a3 3 0 0 1 3 3 3 3 0 0 1 2 5.2 3 3 0 0 1-2.5 5.3A2.5 2.5 0 0 1 12 19',
  memory: 'M4.7 4h14.6c.7 0 1.2.5 1.2 1.2v2.6c0 .7-.5 1.2-1.2 1.2H4.7c-.7 0-1.2-.5-1.2-1.2V5.2c0-.7.5-1.2 1.2-1.2zM5 9v9.5A1.5 1.5 0 0 0 6.5 20h11a1.5 1.5 0 0 0 1.5-1.5V9M10 13h4',
  people: 'M9 5.3a3.2 3.2 0 1 1 0 6.4 3.2 3.2 0 0 1 0-6.4zM3.5 19.5c.6-3.2 2.8-5 5.5-5s4.9 1.8 5.5 5M16.5 7a2.5 2.5 0 1 1 0 5 2.5 2.5 0 0 1 0-5zM15.8 14.6c2.3.1 4.1 1.7 4.7 4.4',
  calendar: 'M5.5 5h13a2 2 0 0 1 2 2v11.5a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2zM3.5 10h17M8 3v4M16 3v4',
  lists: 'M4 6.5l1.5 1.5L8 5.5M4 12.5l1.5 1.5L8 11.5M4 18.5l1.5 1.5L8 17.5M11 7h9M11 13h9M11 19h9',
  podcasts: 'M12 3a3 3 0 0 1 3 3v5a3 3 0 0 1-6 0V6a3 3 0 0 1 3-3zM5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21',
  sleep: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z',
  models: 'M7.8 6h8.4c1 0 1.8.8 1.8 1.8v8.4c0 1-.8 1.8-1.8 1.8H7.8c-1 0-1.8-.8-1.8-1.8V7.8C6 6.8 6.8 6 7.8 6zM10.1 9.5h3.8c.3 0 .6.3.6.6v3.8c0 .3-.3.6-.6.6h-3.8a.6.6 0 0 1-.6-.6v-3.8c0-.3.3-.6.6-.6zM9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3',
  settings: 'M4 7h9M17 7h3M4 17h3M11 17h9M15 5a2 2 0 1 1 0 4 2 2 0 0 1 0-4zM9 15a2 2 0 1 1 0 4 2 2 0 0 1 0-4z',
}

export function NavIcon({ name }: { name: string }) {
  return (
    <svg className="nav-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={paths[name]} />
    </svg>
  )
}

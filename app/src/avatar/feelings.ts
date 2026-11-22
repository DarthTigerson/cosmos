// How each of her feelings shows: a face built from parts, and a manner.
//
// Her model (a VRoid one) has, beside its five whole-face presets, the
// brows, eyes and mouth of each preset as separate shapes, and a few
// more: wide eyes, a small mouth, a large one. The presets move all three
// together into one stock face, which is what made her look stiff. A
// real face mixes: worried brows over a soft mouth, a smile that has not
// reached the eyes. So each feeling here names its parts, and they blend
// with each other and with her mood.

// The shapes on her model, by their names there.
export const PARTS = [
  'Fcl_BRW_Angry', 'Fcl_BRW_Fun', 'Fcl_BRW_Joy', 'Fcl_BRW_Sorrow', 'Fcl_BRW_Surprised',
  'Fcl_EYE_Angry', 'Fcl_EYE_Fun', 'Fcl_EYE_Joy', 'Fcl_EYE_Sorrow', 'Fcl_EYE_Spread', 'Fcl_EYE_Surprised',
  'Fcl_MTH_Angry', 'Fcl_MTH_Fun', 'Fcl_MTH_Joy', 'Fcl_MTH_Sorrow', 'Fcl_MTH_Surprised', 'Fcl_MTH_Small', 'Fcl_MTH_Large',
] as const
export type Part = (typeof PARTS)[number]
export type Look = Partial<Record<Part, number>>

// How a feeling carries itself, beyond the face. Each is a small number
// added to or multiplying her usual behaviour.
export interface Manner {
  // Head tilt to the side and chin down (+) or up (-), in radians.
  tilt: number
  chin: number
  // How often her eyes leave you, and how often she blinks, times usual.
  away: number
  blinks: number
  // Eyes lowered, -1 to 1 like the gaze.
  down: number
}

interface Feeling {
  look: Look
  manner: Partial<Manner>
}

const feelings: Record<string, Feeling> = {
  // Good feelings: the eyes soften before the mouth moves much.
  glad: { look: { Fcl_BRW_Joy: 0.35, Fcl_EYE_Fun: 0.35, Fcl_MTH_Fun: 0.5 }, manner: { tilt: 0.03 } },
  warmth: { look: { Fcl_BRW_Fun: 0.3, Fcl_EYE_Fun: 0.3, Fcl_MTH_Fun: 0.35 }, manner: { tilt: 0.05, away: 0.7, blinks: 0.85 } },
  tenderness: { look: { Fcl_BRW_Sorrow: 0.18, Fcl_EYE_Fun: 0.35, Fcl_MTH_Fun: 0.25 }, manner: { tilt: 0.07, chin: 0.03, away: 0.6, blinks: 0.8 } },
  amusement: { look: { Fcl_BRW_Fun: 0.45, Fcl_EYE_Joy: 0.4, Fcl_MTH_Joy: 0.45 }, manner: { tilt: 0.04, away: 1.1 } },
  pride: { look: { Fcl_BRW_Fun: 0.25, Fcl_EYE_Fun: 0.2, Fcl_MTH_Fun: 0.45 }, manner: { chin: -0.04, away: 0.7 } },
  hope: { look: { Fcl_BRW_Surprised: 0.2, Fcl_EYE_Fun: 0.18, Fcl_MTH_Fun: 0.25 }, manner: { chin: -0.02, tilt: 0.02 } },
  curiosity: { look: { Fcl_BRW_Surprised: 0.35, Fcl_EYE_Spread: 0.22, Fcl_MTH_Small: 0.18 }, manner: { tilt: 0.09, away: 0.8 } },
  // Troubled feelings. Her fringe hides most of her brows, so the eyes
  // and mouth have to carry these more than a bare face would.
  concern: { look: { Fcl_BRW_Sorrow: 0.5, Fcl_EYE_Sorrow: 0.25, Fcl_MTH_Sorrow: 0.25 }, manner: { tilt: 0.04, chin: 0.02 } },
  worry: { look: { Fcl_BRW_Sorrow: 0.65, Fcl_EYE_Sorrow: 0.35, Fcl_MTH_Sorrow: 0.35, Fcl_MTH_Small: 0.2 }, manner: { chin: 0.03, away: 1.4, blinks: 1.3 } },
  unease: { look: { Fcl_BRW_Sorrow: 0.4, Fcl_BRW_Angry: 0.12, Fcl_EYE_Sorrow: 0.25, Fcl_EYE_Angry: 0.08, Fcl_MTH_Sorrow: 0.2, Fcl_MTH_Small: 0.35 }, manner: { chin: 0.02, away: 1.8, blinks: 1.5 } },
  sadness: { look: { Fcl_BRW_Sorrow: 0.8, Fcl_EYE_Sorrow: 0.6, Fcl_MTH_Sorrow: 0.65 }, manner: { chin: 0.08, away: 1.3, blinks: 0.7, down: 0.08 } },
  hurt: { look: { Fcl_BRW_Sorrow: 0.7, Fcl_EYE_Sorrow: 0.5, Fcl_MTH_Sorrow: 0.45, Fcl_MTH_Small: 0.2 }, manner: { chin: 0.05, away: 1.6, down: 0.06 } },
  anger: { look: { Fcl_BRW_Angry: 0.6, Fcl_EYE_Angry: 0.45, Fcl_MTH_Angry: 0.4 }, manner: { chin: 0.03, away: 0.5, blinks: 0.7 } },
  // Readings of her mood when no feeling is fresh.
  bright: { look: { Fcl_BRW_Fun: 0.2, Fcl_EYE_Fun: 0.2, Fcl_MTH_Fun: 0.3 }, manner: { tilt: 0.02 } },
  warm: { look: { Fcl_EYE_Fun: 0.18, Fcl_MTH_Fun: 0.2 }, manner: { tilt: 0.03, away: 0.8 } },
  quiet: { look: { Fcl_EYE_Sorrow: 0.08 }, manner: { chin: 0.03, away: 1.2, blinks: 0.75 } },
  distant: { look: { Fcl_BRW_Sorrow: 0.12 }, manner: { away: 1.6, down: 0.04 } },
  unsure: { look: { Fcl_BRW_Sorrow: 0.3, Fcl_EYE_Sorrow: 0.12, Fcl_MTH_Small: 0.25 }, manner: { tilt: 0.03, away: 1.4, blinks: 1.2 } },
}

export const usual: Manner = { tilt: 0, chin: 0, away: 1, blinks: 1, down: 0 }

// How strongly a feeling shows by its age: plainly when it is new, then
// settling into the background over twenty minutes or so, as a feeling
// does once it has been had. Without a time it shows at a steady middle.
function strength(since?: string): number {
  if (!since) return 0.6
  const minutes = (Date.now() - Date.parse(since)) / 60_000
  if (!Number.isFinite(minutes) || minutes < 0) return 0.6
  return 0.3 + 0.7 * Math.exp(-minutes / 15)
}

export interface Shown {
  look: Look
  manner: Manner
}

// Her face and manner for a feeling and her mood. Warmth adds a little
// softness to any face; low energy makes everything smaller and slower;
// low confidence lifts the inner brows and draws the eyes away.
export function shownFor(feeling: string, mood: { warmth: number; energy: number; confidence: number }, since?: string): Shown {
  const f = feelings[feeling]
  const k = f ? strength(since) * (0.75 + mood.energy * 0.5) : 0
  const look: Look = {}
  const add = (p: Part, v: number) => {
    look[p] = Math.min(1, (look[p] ?? 0) + v)
  }
  if (f) for (const [p, v] of Object.entries(f.look) as [Part, number][]) add(p, v * k)
  const soft = Math.max(0, mood.warmth - 0.55)
  add('Fcl_EYE_Fun', soft * 0.35)
  add('Fcl_MTH_Fun', soft * 0.4)
  const doubt = Math.max(0, 0.45 - mood.confidence)
  add('Fcl_BRW_Sorrow', doubt * 0.6)
  const m = f?.manner ?? {}
  const scale = (v: number | undefined, base: number) => base + ((v ?? base) - base) * (f ? k : 0)
  return {
    look,
    manner: {
      tilt: scale(m.tilt, 0),
      chin: scale(m.chin, 0) + (0.5 - mood.energy) * 0.04,
      away: scale(m.away, 1) * (1 + doubt),
      blinks: scale(m.blinks, 1),
      down: scale(m.down, 0),
    },
  }
}

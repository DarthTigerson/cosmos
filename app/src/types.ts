// Shapes of the core's JSON. They mirror the Go structs in core/internal.

export interface Mood {
  warmth: number
  energy: number
  confidence: number
  feeling: string
  // When the feeling came, RFC 3339.
  feeling_at?: string
}

export interface State {
  name: string
  user: string
  state: 'awake' | 'asleep'
  awake_since: string
  mood: Mood
  feeling: string
  pending_proposals: number
}

export interface EventMeta {
  run_id?: number
  verified?: boolean
  kind?: string
  reason?: string
  emotion?: string
  intensity?: number
  touches?: string
  about?: string
  // A picture that goes with it, and what answering it settles.
  image_id?: number
  // Pictures the user sent with a message.
  image_ids?: number[]
  subject?: string
}

// One sighting of a face; person_id -1 is the user.
export interface Face {
  id: number
  at: string
  image_id: number
  person_id: number
  group_id: number
  score: number
  status: 'confirmed' | 'known' | 'maybe' | 'unknown'
  seconds: number
}

// Someone seen but not known yet: every sighting and the best picture.
export interface UnknownGroup {
  id: number
  faces: Face[]
  cover: number
  seen: number
  seconds: number
  last: string
}

// Which of her senses are set up.
export interface Senses {
  faces: boolean
  voices: boolean
  missing: string[]
  knows_you: boolean
  knows_her: boolean
  unknown: number
}

export interface StreamEvent {
  id: number
  at: string
  kind: 'user_msg' | 'nova_msg' | 'initiative' | 'appraisal' | 'journal' | 'sleep' | 'system' | 'tool' | 'mistake'
  actor: string
  text: string
  importance: number
  meta?: EventMeta
  session_id?: number
}

export interface Session {
  id: number
  started_at: string
  ended_at: string
  title: string
  summary: string
  slept_at: string
  started_by: 'user' | 'nova'
  messages: number
}

export interface SessionsView {
  current: number
  open: Session[]
}

export interface SessionDetail {
  session: Session
  messages: StreamEvent[]
}

export interface KeptSession extends Session {
  remembered: Memory[]
  mistakes: string[]
}

export interface Step {
  name: string
  tier: 'main' | 'worker' | 'tool' | 'code' | 'voice'
  ok: boolean
  ms: number
  note?: string
}

export interface Run {
  id: number
  at: string
  intent: string
  mode: string
  steps: Step[]
  rewrites: number
  verified: boolean
  ms: number
  request: string
}

export interface Intention {
  id: number
  kind: string
  text: string
  reason: string
  trigger_at: string
  expires_at: string
  status: 'open' | 'sent' | 'done' | 'expired'
}

export interface Today {
  state: State
  messages: StreamEvent[]
  intentions: Intention[]
  last_run?: Run
  session?: Session
  earlier?: Session[]
}

export interface Value {
  id: number
  text: string
  origin: 'seed' | 'hers'
}

export interface Mind {
  state: State
  stream: StreamEvent[]
  values: Value[]
  pending_proposals: number
  learned: Memory[]
  intentions: Intention[]
}

export interface Memory {
  id: number
  kind: 'fact' | 'reflection'
  category: string
  content: string
  importance: number
  created_at: string
  last_used_at: string
  archived: boolean
  source: string
  history?: Past[]
}

// One wording a memory used to have, and when she learned it.
export interface Past {
  content: string
  since: string
}

export interface Voice {
  persona: string
  examples: string
  default_persona: string
  default_examples: string
}

export interface Proposal {
  id: number
  kind: 'value' | 'writing' | 'planning' | 'bug'
  target: string
  before: string
  after: string
  reason: string
  status: 'pending' | 'approved' | 'declined'
  decided_at: string
}

export interface Tidy {
  id: number
  at: string
  summary: string
  undone: boolean
}

export interface SleepView {
  state: State
  last_sleep_at?: string
  journal?: string
  reflections: Memory[]
  tidied: Tidy[]
  pending: Proposal[]
  decided: Proposal[]
  sessions: KeptSession[]
  nights: string[]
  viewing?: string
  questions: { id: number; text: string; reason: string; status: string }[]
}

export interface Person {
  id: number
  name: string
  aliases: string[]
  relationship: string
  closeness: number
  notes: string
  last_mentioned: string
  // Fields the user set by hand: "name", "relationship", "aliases", "closeness".
  locked: string[]
  // For someone she cannot place yet: the last thing you said about them.
  heard?: string
}

export interface Connection {
  id: number
  name: string
  relationship: string
  shared: number
}

export interface PersonDetail {
  person: Person
  memories: Memory[]
  moments: StreamEvent[]
  mentions: number
  weeks: number[]
  connections: Connection[]
  user: string
  companion: string
  faces: Face[]
  voice: boolean
}

export interface Feed {
  id: number
  url: string
  title: string
  description: string
  has_art: boolean
  added_at: string
  checked_at: string
  error: string
}

export interface Episode {
  id: number
  feed_id: number
  title: string
  published_at: string
  duration_sec: number
  status: 'waiting' | 'queued' | 'downloading' | 'transcribing' | 'reading' | 'thinking' | 'done' | 'failed'
  stage_note: string
  error: string
  summary: string
  takeaways: string[]
  thoughts: string[]
  share: string
  notes: { at: string; text: string }[]
  has_transcript: boolean
  started_at: string
  finished_at: string
}

export interface PodcastsView {
  enabled: boolean
  missing: string
  feeds: Feed[]
  current: Episode | null
  queued: Episode[]
  done: Episode[]
  failed: Episode[]
  listened: Record<string, number>
  // Episodes of each show she has not taken on, by show id.
  unheard: Record<string, number>
}

export type BookChapter = { n: number; title: string; words: number; parts: number; notes: string[]; digest: string; learned: string[] }

export type Book = {
  id: number
  title: string
  author: string
  kind: 'fiction' | 'nonfiction'
  has_cover: boolean
  added_at: string
  started_at: string
  finished_at: string
  status: 'waiting' | 'queued' | 'opening' | 'reading' | 'digesting' | 'thinking' | 'done' | 'failed'
  // "chapter 4 of 7 · part 51 of 116" while reading, "chapter 3 of 7" while digesting.
  stage_note: string
  error: string
  words: number
  parts: number
  // Parts read so far.
  part: number
  summary: string
  thoughts: string[]
  share: string
  chapters: BookChapter[]
  // Read, or being read, during her sleep.
  asleep: boolean
}

export type BooksView = {
  enabled: boolean
  // There is a current book but she is waiting because you are talking.
  paused: boolean
  books: Book[]
  current: Book | null
  queued: Book[]
  done: Book[]
  failed: Book[]
}

export interface ListItem {
  id: number
  list: string
  text: string
  done: boolean
  created_at: string
  done_at: string
}

export interface CalendarItem {
  id: number
  kind: 'event' | 'reminder'
  title: string
  at: string
  duration_min: number
  location: string
  repeat: '' | 'daily' | 'weekly' | 'monthly' | 'yearly'
}

export interface Launch {
  binary: string
  model_path: string
  mmproj?: string
  context_size: number
  gpu_layers: number
}

export interface Endpoint {
  base_url: string
  model: string
  api_key?: string
  launch?: Launch | null
}

export interface Config {
  user_name: string
  companion_name: string
  platform_name: string
  hear_model: string
  hear_language: string
  name_sounds_like: string[] | null
  listen_window: number
  hear_pause: number
  room_minutes: number
  remember_faces: boolean
  answer_others: boolean
  unknown_face_days: number
  face_sure: number
  face_maybe: number
  voice_sure: number
  location: string
  main: Endpoint
  worker: Endpoint
  sleeper: Endpoint
  embed: Endpoint
  planning: 'auto' | 'once' | 'steps'
  verify_with_model: boolean
  verify_claims: boolean
  quiet_from: number
  quiet_to: number
  initiative_budget: number
  cooldown_minutes: number
  brief_hour: number
  sleep_hour: number
  weather_watch: boolean
  web_search: boolean
  podcasts: boolean
}

export type TierName = 'main' | 'worker' | 'sleeper' | 'embed'

export interface TierView {
  tier: TierName
  endpoint: Endpoint
  shared_with?: string
  status: { state: 'running' | 'starting' | 'stopped' | 'resting' | 'failed' | 'external'; error?: string; log?: string }
}

export interface ModelFile {
  name: string
  path: string
  size: number
}

export interface ModelsView {
  tiers: TierView[]
  binary: string
  files: ModelFile[]
  folders: string[]
}

export interface Signal {
  kind: 'step' | 'message' | 'mood' | 'state' | 'proposals' | 'sleep' | 'sessions' | 'lists' | 'podcasts' | 'books' | 'writing' | 'memories' | 'people' | 'interrupted'
  data: unknown
}

export interface StepSignal {
  name: string
  tier: Step['tier']
  state: 'running' | 'done'
  ok?: boolean
  ms?: number
}

// The Mac itself, read by the Electron shell (electron/system.cjs).
export interface MemoryUsage {
  usedBytes: number
  totalBytes: number
  swapUsedBytes: number
  compressedBytes: number
  pressure: 'normal' | 'warn' | 'critical'
}

export interface MemoryBreakdown {
  appBytes: number
  coreBytes: number
  models: { port: number; bytes: number }[]
}

export interface BatteryStatus {
  percent: number
  charging: boolean
  pluggedIn: boolean
  minutesLeft: number | null
}

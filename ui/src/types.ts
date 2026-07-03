export interface Source {
  port: number
  network: string
}

export interface Framing {
  mode: string
  delimiter?: string
  prefixLen?: number
  maxBytes?: number
}

export interface ParserCfg {
  format: string
}

export interface Filter {
  expression: string
}

export interface SinkCfg {
  type: string
  url?: string
  subject?: string
}

export interface ListenerDefinition {
  name: string
  source: Source
  framing: Framing
  parser: ParserCfg
  filter?: Filter
  sink: SinkCfg
}

export interface TapEvent {
  ts: string
  dropped: boolean
  fields: Record<string, unknown>
  meta: Record<string, string>
  raw: string
}

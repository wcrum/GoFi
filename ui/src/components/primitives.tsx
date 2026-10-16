// Thin compatibility adapters around Grafana's supported component library.
import { isValidElement, type ButtonHTMLAttributes, type HTMLAttributes, type InputHTMLAttributes, type ReactElement, type ReactNode } from 'react'
import {
  Alert as GrafanaAlert, Badge as GrafanaBadge, Button as GrafanaButton,
  Checkbox as GrafanaCheckbox, Combobox, Field, Input as GrafanaInput,
  PanelContainer, Spinner as GrafanaSpinner,
} from '@grafana/ui'

export type BadgeColor = 'gray' | 'blue' | 'green' | 'purple' | 'orange' | 'yellow' | 'red'
export interface SelectOption { value: string; label: string }
const badgeColors = { gray: 'darkgrey', blue: 'blue', green: 'green', purple: 'purple', orange: 'orange', yellow: 'orange', red: 'red' } as const
export function Badge({ text, color = 'gray', className = '' }: { text: string; color?: BadgeColor; className?: string }) {
  return <GrafanaBadge text={text} color={badgeColors[color]} className={className} />
}
export function Card({ padding = 'md', className = '', ...props }: HTMLAttributes<HTMLDivElement> & { padding?: 'none' | 'sm' | 'md' | 'lg' }) {
  return <PanelContainer {...props} className={`${padding === 'none' ? 'p-0' : padding === 'sm' ? 'p-3' : padding === 'lg' ? 'p-6' : 'p-4'} ${className}`} />
}
export function Spinner({ size = '24px' }: { size?: string }) { return <span role="status" aria-label="Loading"><GrafanaSpinner size={size === '16px' ? 'md' : 'xl'} /></span> }
export function Input({ className = '', size: _size, width: _width, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <GrafanaInput {...props} className={`min-w-0 w-full ${className}`} />
}
export function Select({ value, onChange, options, disabled, id, 'aria-label': ariaLabel, 'aria-labelledby': ariaLabelledBy }: { value: string; onChange: (value: string) => void; options: SelectOption[]; disabled?: boolean; id?: string; 'aria-label'?: string; 'aria-labelledby'?: string }) {
  return <Combobox value={value} options={options} onChange={option => onChange(option.value)} disabled={disabled} id={id} aria-label={ariaLabel} aria-labelledby={ariaLabelledBy} />
}
export function FormField({ label, children, className = '' }: { label: string; children: ReactNode; className?: string }) {
  const child = (isValidElement(children) ? children : <div>{children}</div>) as ReactElement<Record<string, unknown>>
  return <div className={className}><Field label={label} noMargin>{child}</Field></div>
}
export function DetailSection({ title, children }: { title: string; children: ReactNode }) {
  return <section className="mb-5"><h3 className="mb-4 text-xs font-semibold uppercase tracking-wider text-g-text">{title}</h3>{children}</section>
}
export function PanelHeader({ title, onClose, actions }: { title: string; onClose: () => void; actions?: ReactNode }) {
  return <div className="flex items-center gap-3 border-b border-g-border-weak p-4"><h2 className="text-sm font-semibold flex-1">{title}</h2>{actions}<GrafanaButton variant="secondary" fill="text" size="sm" aria-label="Close panel" onClick={onClose}>×</GrafanaButton></div>
}
export function Alert({ severity, title, children }: { severity: 'error' | 'warning' | 'info' | 'success'; title?: string; children: ReactNode }) {
  return <GrafanaAlert severity={severity} title={title ?? (severity === 'error' ? 'Error' : 'Information')} bottomSpacing={0}>{children}</GrafanaAlert>
}
export function TagsInput({ value, onChange, disabled, placeholder }: { value: string[]; onChange: (values: string[]) => void; disabled?: boolean; placeholder?: string }) {
  return <Input value={value.join(', ')} disabled={disabled} placeholder={placeholder ?? 'Comma-separated values'} onChange={event => onChange(event.target.value.split(',').map(entry => entry.trim()))} />
}
export function Checkbox({ checked, onChange, label, description, disabled }: { checked: boolean; onChange: (checked: boolean) => void; label: string; description?: string; disabled?: boolean }) {
  return <GrafanaCheckbox value={checked} onChange={event => onChange(event.currentTarget.checked)} label={label} description={description} disabled={disabled} />
}
export function Button({ children, icon, className = '', variant = 'secondary', size = 'sm', fill, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { icon?: ReactNode; variant?: string; size?: string; fill?: 'solid' | 'outline' | 'text' }) {
  const buttonVariant = variant === 'primary' || variant === 'destructive' || variant === 'success' ? variant : 'secondary'
  return <GrafanaButton {...props} variant={buttonVariant} fill={fill ?? (variant === 'text' ? 'text' : buttonVariant === 'secondary' ? 'outline' : 'solid')} size={size === 'md' || size === 'lg' ? size : 'sm'} className={className}>{icon}{children}</GrafanaButton>
}

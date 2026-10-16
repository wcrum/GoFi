import type { IconName } from '@grafana/data'
import { Dropdown, Menu, ToolbarButton } from '@grafana/ui'
import { useColorMode, useThemePreference, type ThemePreference } from './ThemeProvider'

const OPTIONS: Array<{ value: ThemePreference; label: string; icon: IconName; description: string }> = [
  { value: 'system', label: 'System', icon: 'monitor', description: 'Follow the operating system setting' },
  { value: 'light', label: 'Light', icon: 'circle', description: 'Grafana light theme' },
  { value: 'dark', label: 'Dark', icon: 'adjust-circle', description: 'Grafana dark theme' },
]

/** Top-bar theme menu: System, Light, or Dark. */
export function ThemePicker() {
  const mode = useColorMode()
  const [preference, setPreference] = useThemePreference()
  const current = OPTIONS.find(option => option.value === preference) ?? OPTIONS[0]
  const menu = <Menu>
    <Menu.Group label="Theme">
      {OPTIONS.map(option => <Menu.Item key={option.value} label={option.label} description={option.description}
        icon={option.value === preference ? 'check' : option.icon} active={option.value === preference}
        onClick={() => setPreference(option.value)} />)}
    </Menu.Group>
  </Menu>
  const label = `Theme: ${current.label}${preference === 'system' ? ` (${mode})` : ''}`
  return <Dropdown overlay={menu} placement="bottom-end">
    <ToolbarButton icon={mode === 'dark' ? 'adjust-circle' : 'circle'} aria-label={label} tooltip={label} />
  </Dropdown>
}

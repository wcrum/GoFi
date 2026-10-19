import { Dropdown, Menu, ToolbarButton } from '@grafana/ui'
import { useFlowStatuses } from '../../operations-api'
import { navigate, paths } from '../../router'
import { t } from '../../i18n'

/** Top-bar count of open problems across flows; opens a menu of the flows
 * that have them. Hidden while there are none. */
export function ProblemsBadge() {
  const { byName, problems } = useFlowStatuses()
  if (!problems) return null
  const flows = Object.values(byName).filter(status => status.problems > 0).sort((a, b) => b.problems - a.problems)
  const menu = <Menu>
    {flows.map(status => <Menu.Item key={status.name} label={status.name} icon={status.errors ? 'exclamation-circle' : 'exclamation-triangle'}
      description={[
        status.errors && t('gofi.operations.problems-badge.error-count', '{{count}} errors', { count: status.errors, defaultValue_one: '{{count}} error' }),
        status.warnings && t('gofi.operations.problems-badge.warning-count', '{{count}} warnings', { count: status.warnings, defaultValue_one: '{{count}} warning' }),
      ].filter(Boolean).join(' · ')}
      onClick={() => navigate(paths.flow(status.name, 'problems'))} />)}
  </Menu>
  return <Dropdown overlay={menu} placement="bottom-end">
    <ToolbarButton icon="exclamation-triangle" variant="default" aria-label={t('gofi.operations.problems-badge.open-problems', '{{count}} open problems', { count: problems, defaultValue_one: '{{count}} open problem' })} tooltip={t('gofi.operations.problems-badge.tooltip', 'Open flow problems')}>{problems}</ToolbarButton>
  </Dropdown>
}

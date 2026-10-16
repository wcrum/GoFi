import SandboxManager from '../components/SandboxManager'
import { Page } from '../shell/Page'

export default function LibrariesPage() {
  return <Page title="Python libraries" icon="brackets-curly" subTitle="Write reusable ingress hooks, parsers, transformers, and sinks. GoFi runs each library in a separate worker; registered functions appear in the flow builder.">
    <SandboxManager />
  </Page>
}

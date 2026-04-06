import { onMounted, ref } from 'vue'
import { useClientService } from '@opencloud-eu/web-pkg'
import { z } from 'zod'

const groupSchema = z.object({
  name: z.string(),
  file_count: z.number().optional(),
  total_size: z.number().optional()
})

const groupsSchema = z.object({
  groups: z.array(groupSchema)
})

export type KnowledgeGroup = z.infer<typeof groupSchema>

/**
 * Loads the list of existing Synaplan knowledge groups via the
 * backend proxy on mount. Failures are swallowed — the group list
 * is optional, callers fall back to "user types a new name".
 */
export function useKnowledgeGroups() {
  const { httpAuthenticated } = useClientService()

  const groups = ref<KnowledgeGroup[]>([])
  const loading = ref(false)
  const error = ref('')

  async function load() {
    loading.value = true
    error.value = ''
    try {
      const { data } = await httpAuthenticated.get('/api/synaplan/knowledge/groups', {
        schema: groupsSchema
      })
      groups.value = data.groups
    } catch (e) {
      console.warn('synaplan knowledge groups: fetch failed', e)
      error.value = e instanceof Error && e.message ? e.message : 'failed to load groups'
    } finally {
      loading.value = false
    }
  }

  onMounted(load)

  return { groups, loading, error, reload: load }
}

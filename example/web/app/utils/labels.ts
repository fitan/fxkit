export function articleStatusLabel(status: string): string {
  switch (status) {
    case 'draft':
      return '草稿'
    case 'indexed':
      return '已索引'
    case 'published':
      return '已发布'
    default:
      return status
  }
}

export function articleStatusColor(status: string): 'success' | 'warning' | 'neutral' {
  if (status === 'indexed') return 'success'
  if (status === 'draft') return 'warning'
  return 'neutral'
}

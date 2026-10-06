/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
interface TaskMobileSummaryField {
  id: string
  label: string
  primaryOnly?: boolean
}

export const TASK_MOBILE_SUMMARY_FIELDS: readonly TaskMobileSummaryField[] = [
  { id: 'submit_time', label: 'Created At' },
  { id: 'finish_time', label: 'Finished At' },
  { id: 'model', label: 'Model' },
  { id: 'cost', label: 'Cost' },
  { id: 'user', label: 'User', primaryOnly: true },
  { id: 'duration', label: 'Duration', primaryOnly: true },
  { id: 'progress', label: 'Progress' },
  { id: 'resolution', label: 'Resolution' },
  { id: 'duration_seconds', label: 'Duration (seconds)' },
  { id: 'has_reference_video', label: 'Has Reference Video' },
  { id: 'consumed_tokens', label: 'Consumed Tokens' },
  { id: 'channel_id', label: 'Channel', primaryOnly: true },
  { id: 'plugin', label: 'Plugin' },
  { id: 'artifacts', label: 'Artifacts' },
]

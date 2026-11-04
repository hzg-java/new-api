import { useId } from 'react'

import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

type MaterialSelectProps = {
  label: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
}

export function MaterialSelect(props: MaterialSelectProps) {
  const id = useId()
  return (
    <div className='grid min-w-0 gap-2'>
      <Label htmlFor={id}>{props.label}</Label>
      <Select
        items={props.options}
        value={props.value}
        onValueChange={(value) => {
          if (value !== null) props.onChange(value)
        }}
      >
        <SelectTrigger
          id={id}
          className='min-h-8 w-full min-w-0 whitespace-normal data-[size=default]:h-auto *:data-[slot=select-value]:line-clamp-none'
        >
          <SelectValue className='min-w-0 break-all whitespace-normal' />
        </SelectTrigger>
        <SelectContent>
          {props.options.map((option) => (
            <SelectItem
              key={option.value}
              value={option.value}
              className='[&_[data-slot=select-item-text]]:min-w-0 [&_[data-slot=select-item-text]]:shrink [&_[data-slot=select-item-text]]:break-all [&_[data-slot=select-item-text]]:whitespace-normal'
            >
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

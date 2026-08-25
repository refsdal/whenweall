import * as React from 'react'
import { cva, type VariantProps } from 'class-variance-authority'
import { Slot } from 'radix-ui'

import { cn } from '#/lib/utils'

/**
 * whenweall buttons are pills. The filled variant uses `--primary-strong` rather than the raw
 * brand `--primary` so white label text clears 4.5:1; hover lifts the button and lights an
 * ember glow underneath instead of shifting the fill.
 */
const buttonVariants = cva(
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-full text-sm font-medium whitespace-nowrap transition-[color,background-color,border-color,box-shadow,transform] duration-200 outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        default:
          'bg-primary-strong text-primary-foreground shadow-[0_1px_2px_-1px_hsl(var(--shadow-color)/0.4)] hover:-translate-y-px hover:shadow-[0_10px_26px_-12px_var(--primary)] active:translate-y-0',
        destructive:
          'bg-destructive text-destructive-foreground hover:-translate-y-px hover:shadow-[0_10px_26px_-12px_var(--destructive)] active:translate-y-0',
        outline:
          'border border-border bg-card text-foreground shadow-xs hover:border-foreground/25 hover:bg-secondary',
        secondary: 'bg-secondary text-secondary-foreground hover:bg-secondary/70',
        soft: 'bg-accent-soft text-accent-foreground hover:brightness-[0.97] dark:hover:brightness-110',
        ghost: 'text-foreground/80 hover:bg-secondary hover:text-foreground',
        link: 'text-primary-ink underline-offset-4 hover:underline',
      },
      size: {
        default: 'h-10 px-5 has-[>svg]:px-4',
        xs: 'h-7 gap-1 px-2.5 text-xs [&_svg:not([class*="size-"])]:size-3',
        sm: 'h-9 gap-1.5 px-4 has-[>svg]:px-3',
        lg: 'h-12 px-7 text-base has-[>svg]:px-6',
        icon: 'size-10',
        'icon-xs': 'size-7 [&_svg:not([class*="size-"])]:size-3',
        'icon-sm': 'size-9',
        'icon-lg': 'size-12',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

function Button({
  className,
  variant = 'default',
  size = 'default',
  asChild = false,
  ...props
}: React.ComponentProps<'button'> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }) {
  const Comp = asChild ? Slot.Root : 'button'

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Button, buttonVariants }

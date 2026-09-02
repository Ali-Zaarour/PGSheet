import icon from '../assets/appicon.png'

/**
 * The application mark, the same image the window and taskbar use.
 * scripts/icon/appicon.png is the source; genicon copies it here.
 */
export default function Logo({ size = 20 }: { size?: number }) {
  return (
    <img
      src={icon}
      width={size}
      height={size}
      alt=""
      aria-hidden="true"
      className="shrink-0 select-none"
      draggable={false}
    />
  )
}
